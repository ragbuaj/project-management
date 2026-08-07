package httpx_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ragbuaj/project-management/backend/internal/httpx"
)

// stubLimiter stands in for the Redis-backed one. What is under test here is
// the middleware's behavior given each answer a limiter can give, and a real
// Redis cannot be made to fail on demand.
type stubLimiter struct {
	allowed    bool
	retryAfter time.Duration
	err        error
	keys       []string
}

func (s *stubLimiter) Allow(_ context.Context, key string) (bool, time.Duration, error) {
	s.keys = append(s.keys, key)

	return s.allowed, s.retryAfter, s.err
}

// byPath keys the limit on the request path, which is enough to exercise the
// middleware without deciding how real routes will key theirs.
func byPath(r *http.Request) string { return r.URL.Path }

func served(next *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*next = true
		w.WriteHeader(http.StatusNoContent)
	})
}

func TestARequestUnderTheLimitReachesTheHandler(t *testing.T) {
	t.Parallel()

	var reached bool

	lim := &stubLimiter{allowed: true}

	h := httpx.Chain(served(&reached),
		httpx.RequestID,
		httpx.RateLimit(lim, byPath, httpx.FailClosed, discardLogger()),
	)

	if rec := get(t, h, "/auth/login"); rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}

	if !reached {
		t.Error("the handler was not reached")
	}

	if len(lim.keys) != 1 || lim.keys[0] != "/auth/login" {
		t.Errorf("limiter saw keys %v", lim.keys)
	}
}

// docs/api/openapi.yaml declares 429 with a Retry-After header and the
// RATE_LIMITED code. A client that cannot tell how long to wait retries
// immediately, which is the behavior the limit exists to stop.
func TestARefusedRequestAnswers429WithRetryAfter(t *testing.T) {
	t.Parallel()

	var reached bool

	h := httpx.Chain(served(&reached),
		httpx.RequestID,
		httpx.RateLimit(&stubLimiter{retryAfter: 42 * time.Second}, byPath, httpx.FailClosed, discardLogger()),
	)

	rec := get(t, h, "/auth/login")

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}

	if reached {
		t.Error("the handler ran for a refused request")
	}

	if got := rec.Header().Get("Retry-After"); got != "42" {
		t.Errorf("Retry-After = %q, want \"42\"", got)
	}

	body := decodeError(t, rec.Body.Bytes())

	if body.Error.Code != string(httpx.CodeRateLimited) {
		t.Errorf("code = %q, want RATE_LIMITED", body.Error.Code)
	}

	if body.Error.RequestID == "" {
		t.Error("the refusal carries no request id")
	}
}

// Retry-After is whole seconds. Truncating 0.4s to 0 tells a client to retry
// at once, and truncating 1.6s to 1 turns it away again for arriving early.
func TestRetryAfterIsRoundedUpAndNeverZero(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		retryAfter time.Duration
		want       string
	}{
		{0, "1"},
		{100 * time.Millisecond, "1"},
		{time.Second, "1"},
		{1600 * time.Millisecond, "2"},
		{90 * time.Second, "90"},
	} {
		t.Run(tc.retryAfter.String(), func(t *testing.T) {
			t.Parallel()

			var reached bool

			h := httpx.Chain(served(&reached),
				httpx.RequestID,
				httpx.RateLimit(&stubLimiter{retryAfter: tc.retryAfter}, byPath, httpx.FailClosed, discardLogger()),
			)

			if got := get(t, h, "/x").Header().Get("Retry-After"); got != tc.want {
				t.Errorf("Retry-After = %q, want %q", got, tc.want)
			}
		})
	}
}

// docs/nfr.md: when Redis is down the authentication path fails closed,
// because turning off the brute-force guard is worse than refusing logins for
// a while. Everything else keeps serving.
func TestALimiterFailureIsResolvedPerPath(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		onFailure  httpx.OnLimiterFailure
		wantStatus int
		wantServed bool
	}{
		{"authentication fails closed", httpx.FailClosed, http.StatusTooManyRequests, false},
		{"everything else keeps serving", httpx.FailOpen, http.StatusNoContent, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var reached bool

			h := httpx.Chain(served(&reached),
				httpx.RequestID,
				httpx.RateLimit(&stubLimiter{err: errors.New("dial tcp: connection refused")},
					byPath, tc.onFailure, discardLogger()),
			)

			rec := get(t, h, "/auth/login")

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}

			if reached != tc.wantServed {
				t.Errorf("handler reached = %v, want %v", reached, tc.wantServed)
			}
		})
	}
}

// The zero value has to be the safe half: a call site that forgets to choose
// must not silently disable the guard it was added to provide.
func TestTheZeroValueFailsClosed(t *testing.T) {
	t.Parallel()

	var zero httpx.OnLimiterFailure

	if zero != httpx.FailClosed {
		t.Fatal("the zero OnLimiterFailure is not FailClosed")
	}
}

// A failure that says nothing leaves whoever is on call guessing. A failure
// that names the account or the address puts personal data in a line written
// on every request for as long as Redis stays down.
func TestALimiterFailureIsLoggedWithoutTheKey(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	key := func(*http.Request) string { return "account:someone@example.test" }

	var reached bool

	h := httpx.Chain(served(&reached),
		httpx.RequestID,
		httpx.RateLimit(&stubLimiter{err: errors.New("connection refused")},
			key, httpx.FailOpen, capturingLogger(&buf)),
	)

	get(t, h, "/search")

	logged := buf.String()

	if !strings.Contains(logged, "rate limiter unavailable") {
		t.Errorf("the failure was not logged: %s", logged)
	}

	if strings.Contains(logged, "someone@example.test") {
		t.Errorf("the log leaks the key: %s", logged)
	}

	if !strings.Contains(logged, "fail_open") {
		t.Error("the log does not record which half was taken, so nobody can tell what happened next")
	}
}

// A route exempts a request by returning no key. Counting it under "" would
// put every exempt request in one bucket and rate-limit them together.
func TestAnEmptyKeySkipsTheLimit(t *testing.T) {
	t.Parallel()

	var reached bool

	lim := &stubLimiter{}

	h := httpx.Chain(served(&reached),
		httpx.RequestID,
		httpx.RateLimit(lim, func(*http.Request) string { return "" }, httpx.FailClosed, discardLogger()),
	)

	if rec := get(t, h, "/healthz"); rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}

	if !reached {
		t.Error("an exempt request was refused")
	}

	if len(lim.keys) != 0 {
		t.Errorf("the limiter was consulted with %v", lim.keys)
	}
}

// The failed-closed answer still has to carry a usable Retry-After, or a
// client has nothing to obey while Redis is being brought back.
func TestAFailedClosedRefusalStillTellsTheClientWhenToRetry(t *testing.T) {
	t.Parallel()

	var reached bool

	h := httpx.Chain(served(&reached),
		httpx.RequestID,
		httpx.RateLimit(&stubLimiter{err: errors.New("connection refused")},
			byPath, httpx.FailClosed, discardLogger()),
	)

	rec := get(t, h, "/auth/login")

	seconds, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil {
		t.Fatalf("Retry-After = %q: %v", rec.Header().Get("Retry-After"), err)
	}

	if seconds < 1 || seconds > 300 {
		t.Errorf("Retry-After = %d seconds, want something a client will actually wait out", seconds)
	}
}
