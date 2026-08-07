package httpx_test

import (
	"bytes"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ragbuaj/project-management/backend/internal/httpx"
)

// aToken is shaped like the value this server will issue: 32 bytes, base64url,
// no padding. Spelled out rather than borrowed from the production code so a
// change to the shape shows up here as a failure rather than as agreement.
var aToken = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x2a}, 32))

// csrfCall is one request against the middleware. An empty field is absent
// rather than empty, which is the distinction most of these tests turn on.
type csrfCall struct {
	method string
	cookie string
	header string
	bearer string
}

func (c csrfCall) do(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), c.method, "/api/v1/cards", nil)

	if c.cookie != "" {
		req.AddCookie(&http.Cookie{Name: httpx.CSRFCookieName, Value: c.cookie})
	}

	if c.header != "" {
		req.Header.Set(httpx.CSRFHeaderName, c.header)
	}

	if c.bearer != "" {
		req.Header.Set("Authorization", c.bearer)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// guardedBy wraps a handler that records whether it was reached. RequestID is
// included because a refusal carries one, same as every other response here.
func guardedBy(t *testing.T, log *slog.Logger) (http.Handler, *bool) {
	t.Helper()

	reached := new(bool)

	h := httpx.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*reached = true
			w.WriteHeader(http.StatusNoContent)
		}),
		httpx.RequestID,
		httpx.CSRF(log),
	)

	return h, reached
}

// The whole point of the middleware: a cross-site form can make a browser send
// its cookies, but it cannot make it send this header.
func TestAMutatingRequestWithoutTheHeaderIsRefused(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			h, reached := guardedBy(t, discardLogger())

			rec := csrfCall{method: method, cookie: aToken}.do(t, h)

			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", rec.Code)
			}

			if *reached {
				t.Error("the handler ran on a request with no csrf header")
			}

			body := decodeError(t, rec.Body.Bytes())
			if body.Error.Code != string(httpx.CodeForbidden) {
				t.Errorf("code = %q, want FORBIDDEN", body.Error.Code)
			}

			if body.Error.RequestID == "" {
				t.Error("the refusal carries no request id")
			}
		})
	}
}

// A header alone proves nothing: whoever could set it could also invent the
// value. It is the match with a cookie the sender cannot read that carries the
// proof.
func TestAMutatingRequestWithoutTheCookieIsRefused(t *testing.T) {
	t.Parallel()

	h, reached := guardedBy(t, discardLogger())

	rec := csrfCall{method: http.MethodPost, header: aToken}.do(t, h)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}

	if *reached {
		t.Error("the handler ran on a request with no csrf cookie")
	}
}

func TestAMismatchedPairIsRefused(t *testing.T) {
	t.Parallel()

	other := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x2b}, 32))

	h, reached := guardedBy(t, discardLogger())

	rec := csrfCall{method: http.MethodPost, cookie: aToken, header: other}.do(t, h)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}

	if *reached {
		t.Error("the handler ran on a mismatched pair")
	}
}

// A prefix that compares equal for its whole length must not pass. The compare
// is length-sensitive, and this is the case that proves it.
func TestATruncatedTokenIsRefused(t *testing.T) {
	t.Parallel()

	h, reached := guardedBy(t, discardLogger())

	rec := csrfCall{method: http.MethodPost, cookie: aToken, header: aToken[:len(aToken)-1]}.do(t, h)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}

	if *reached {
		t.Error("a truncated token was accepted")
	}
}

func TestAMatchingPairIsLetThrough(t *testing.T) {
	t.Parallel()

	h, reached := guardedBy(t, discardLogger())

	rec := csrfCall{method: http.MethodPost, cookie: aToken, header: aToken}.do(t, h)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204: %s", rec.Code, rec.Body)
	}

	if !*reached {
		t.Error("a matching pair did not reach the handler")
	}
}

// Reading is not changing. A GET that had to carry a token would mean every
// link into the application needed one, which is not something a link can do.
func TestSafeMethodsAreServedWithoutThePair(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			h, reached := guardedBy(t, discardLogger())

			rec := csrfCall{method: method}.do(t, h)

			if rec.Code != http.StatusNoContent {
				t.Errorf("status = %d, want 204", rec.Code)
			}

			if !*reached {
				t.Error("a safe method was refused")
			}
		})
	}
}

// ADR-0005 exempts Bearer callers: that token is not ambient, so a browser
// never attaches it on somebody else's behalf. The scheme name is not case
// sensitive (RFC 9110), and a client spelling it in lower case is still one.
func TestABearerCallerIsExemptInEitherCase(t *testing.T) {
	t.Parallel()

	for _, authorization := range []string{"Bearer pmt_abc", "bearer pmt_abc"} {
		t.Run(authorization, func(t *testing.T) {
			t.Parallel()

			h, reached := guardedBy(t, discardLogger())

			rec := csrfCall{method: http.MethodPost, bearer: authorization}.do(t, h)

			if rec.Code != http.StatusNoContent {
				t.Errorf("status = %d, want 204: %s", rec.Code, rec.Body)
			}

			if !*reached {
				t.Error("a Bearer caller was refused")
			}
		})
	}
}

// The exemption is for Bearer, not for any Authorization header. Basic
// credentials are as ambient as a cookie — a browser resends them on its own
// once it has them, which is exactly what this middleware exists to distrust.
func TestOtherAuthorizationSchemesAreNotExempt(t *testing.T) {
	t.Parallel()

	h, reached := guardedBy(t, discardLogger())

	rec := csrfCall{method: http.MethodPost, bearer: "Basic YWxpY2U6c2VjcmV0"}.do(t, h)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}

	if *reached {
		t.Error("a Basic caller skipped the csrf check")
	}
}

// ADR-0005 asks for failures to be watched, so they have to be findable. The
// tokens themselves must not be in the line.
func TestARefusalIsLoggedWithoutEitherToken(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	h, _ := guardedBy(t, capturingLogger(&buf))

	csrfCall{method: http.MethodPost, cookie: aToken, header: "wrong-" + aToken}.do(t, h)

	logged := buf.String()

	if !strings.Contains(logged, "csrf check failed") {
		t.Errorf("the refusal was not logged: %s", logged)
	}

	if !strings.Contains(logged, "does not match") {
		t.Errorf("the log does not say which half failed: %s", logged)
	}

	if strings.Contains(logged, aToken) {
		t.Errorf("a token value reached the log: %s", logged)
	}
}
