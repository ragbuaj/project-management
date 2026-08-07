package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ragbuaj/project-management/backend/internal/httpx"
)

// The check is only worth anything if it is actually mounted. ADR-0005 puts it
// at the router so that a route cannot be added without it — this is the test
// that says the router really has it, rather than that the middleware would
// work if somebody remembered to use it.
func TestAMutatingRequestIsRefusedBeforeItReachesAnyRoute(t *testing.T) {
	t.Parallel()

	var reached bool

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/anything", func(w http.ResponseWriter, r *http.Request) {
		reached = true
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/anything", nil)

	apiHandler(mux, slog.New(slog.DiscardHandler)).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 — the csrf check is not mounted", rec.Code)
	}

	if reached {
		t.Error("the request reached a route without presenting the csrf pair")
	}
}

// And the way out of that refusal has to exist: a client that has never been
// here gets its token from the first safe request it makes.
func TestASafeRequestComesBackWithAToken(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {})

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)

	apiHandler(mux, slog.New(slog.DiscardHandler)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	for _, line := range rec.Header().Values("Set-Cookie") {
		cookie, err := http.ParseSetCookie(line)
		if err != nil {
			t.Fatalf("parse Set-Cookie %q: %v", line, err)
		}

		if cookie.Name == httpx.CSRFCookieName {
			return
		}
	}

	t.Error("no csrf cookie came back, so nothing can ever make a mutating request")
}
