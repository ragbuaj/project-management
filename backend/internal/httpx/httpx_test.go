package httpx_test

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ragbuaj/project-management/backend/internal/httpx"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func listenLocal(t *testing.T) net.Listener {
	t.Helper()

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	return ln
}

func TestHealth(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)

	httpx.Health().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	if got := rec.Body.String(); got != `{"status":"ok"}` {
		t.Errorf("body = %q", got)
	}
}

// A server without timeouts holds hanging connections until it runs out of
// file descriptors, and that is invisible until it happens. This test is what
// stops one of them being forgotten when NewServer changes.
func TestNewServerPopulatesEveryTimeout(t *testing.T) {
	t.Parallel()

	srv := httpx.NewServer(http.NewServeMux(), httpx.Timeouts{
		Read:  1 * time.Second,
		Write: 2 * time.Second,
		Idle:  3 * time.Second,
	})

	tests := map[string]struct {
		got  time.Duration
		want time.Duration
	}{
		"ReadTimeout":       {srv.ReadTimeout, 1 * time.Second},
		"ReadHeaderTimeout": {srv.ReadHeaderTimeout, 1 * time.Second},
		"WriteTimeout":      {srv.WriteTimeout, 2 * time.Second},
		"IdleTimeout":       {srv.IdleTimeout, 3 * time.Second},
	}

	for name, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", name, tc.got, tc.want)
		}
	}
}

func TestServeAnswersThenStopsWhenContextIsCanceled(t *testing.T) {
	t.Parallel()

	ln := listenLocal(t)

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", httpx.Health())

	srv := httpx.NewServer(mux, httpx.Timeouts{
		Read:  5 * time.Second,
		Write: 5 * time.Second,
		Idle:  5 * time.Second,
	})

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)

	go func() {
		done <- httpx.Serve(ctx, srv, ln, 5*time.Second, discardLogger())
	}()

	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"http://"+ln.Addr().String()+"/healthz",
		nil,
	)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	// SIGTERM from Docker arrives here as a canceled context.
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() returned an error while stopping: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve() did not stop within 10s of the context being canceled")
	}
}

// A listen failure must surface instead of hanging. Here the listener is
// closed before Serve ever sees it.
func TestServeReturnsAnErrorOnADeadListener(t *testing.T) {
	t.Parallel()

	ln := listenLocal(t)

	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	srv := httpx.NewServer(http.NewServeMux(), httpx.Timeouts{
		Read:  time.Second,
		Write: time.Second,
		Idle:  time.Second,
	})

	done := make(chan error, 1)

	go func() {
		done <- httpx.Serve(t.Context(), srv, ln, time.Second, discardLogger())
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Serve() = nil, want an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve() hung on an already-closed listener")
	}
}
