package httpx_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ragbuaj/project-management/backend/internal/httpx"
)

func okCheck(name string) httpx.ReadyCheck {
	return httpx.ReadyCheck{
		Name:  name,
		Probe: func(context.Context) error { return nil },
	}
}

func failingCheck(name string, err error) httpx.ReadyCheck {
	return httpx.ReadyCheck{
		Name:  name,
		Probe: func(context.Context) error { return err },
	}
}

func decodeReady(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}

	return body
}

func TestReadyAllChecksPass(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil)

	httpx.Ready(discardLogger(), time.Second, okCheck("postgres")).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := decodeReady(t, rec)

	if body["status"] != "ok" {
		t.Errorf("status field = %v, want ok", body["status"])
	}

	checks, _ := body["checks"].(map[string]any)
	if checks["postgres"] != "ok" {
		t.Errorf("checks.postgres = %v, want ok", checks["postgres"])
	}
}

func TestReadyOneFailingCheckMakesTheWholeProbeFail(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil)

	handler := httpx.Ready(discardLogger(), time.Second,
		okCheck("postgres"),
		failingCheck("elsewhere", errors.New("down")),
	)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	body := decodeReady(t, rec)

	if body["status"] != "unavailable" {
		t.Errorf("status field = %v, want unavailable", body["status"])
	}

	// A failing check must not hide the passing ones — that is what makes the
	// response useful during an incident.
	checks, _ := body["checks"].(map[string]any)
	if checks["postgres"] != "ok" {
		t.Errorf("checks.postgres = %v, want ok", checks["postgres"])
	}

	if checks["elsewhere"] != "unavailable" {
		t.Errorf("checks.elsewhere = %v, want unavailable", checks["elsewhere"])
	}
}

// Error messages from a database driver carry host names, user names, and
// sometimes the query. None of that belongs in a response served to whoever
// can reach /readyz.
func TestReadyNeverLeaksTheUnderlyingError(t *testing.T) {
	t.Parallel()

	const detail = "dial tcp 10.0.0.7:5432: connect: connection refused"

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil)

	httpx.Ready(discardLogger(), time.Second,
		failingCheck("postgres", errors.New(detail)),
	).ServeHTTP(rec, req)

	if got := rec.Body.String(); strings.Contains(got, "10.0.0.7") ||
		strings.Contains(got, "connection refused") {
		t.Fatalf("response body leaks driver detail: %s", got)
	}
}

// A probe that hangs must not hang the endpoint: the orchestrator polls on an
// interval, and probes that outlive it pile up instead of failing once.
func TestReadyBoundsSlowProbes(t *testing.T) {
	t.Parallel()

	slow := httpx.ReadyCheck{
		Name: "postgres",
		Probe: func(ctx context.Context) error {
			<-ctx.Done()

			return ctx.Err()
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil)

	done := make(chan struct{})

	go func() {
		httpx.Ready(discardLogger(), 50*time.Millisecond, slow).ServeHTTP(rec, req)
		close(done)
	}()

	select {
	case <-done:
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Ready() did not return once the probe timeout elapsed")
	}
}
