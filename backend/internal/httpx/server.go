// Package httpx assembles the HTTP server and the middleware shared across
// modules. It holds no business logic — that belongs to the services inside
// each module.
package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Timeouts bounds how long each phase of a request may live.
//
// A zero value means unbounded. A server without bounds holds hanging
// connections until it runs out of file descriptors, and the symptom only
// appears once it has already happened.
type Timeouts struct {
	Read  time.Duration
	Write time.Duration
	Idle  time.Duration
}

// NewServer builds an http.Server with every timeout populated.
//
// It does not take an address: the caller supplies an already-bound listener,
// so a bind failure happens before anything is logged as "listening".
func NewServer(h http.Handler, t Timeouts) *http.Server {
	return &http.Server{
		Handler: h,
		// ReadHeaderTimeout is set separately as the guard against Slowloris,
		// which holds connections open by dripping headers one byte at a time.
		ReadHeaderTimeout: t.Read,
		ReadTimeout:       t.Read,
		WriteTimeout:      t.Write,
		IdleTimeout:       t.Idle,
	}
}

// Serve serves ln until ctx is canceled, then shuts the server down within
// the grace period. In-flight requests get a chance to finish; those that
// overrun the grace period are cut off.
func Serve(ctx context.Context, srv *http.Server, ln net.Listener, grace time.Duration, log *slog.Logger) error {
	serveErr := make(chan error, 1)

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- fmt.Errorf("serve: %w", err)

			return
		}

		serveErr <- nil
	}()

	log.Info("http server listening", slog.String("addr", ln.Addr().String()))

	select {
	case err := <-serveErr:
		// Failed before anyone asked it to stop — a closed listener, for
		// instance.
		return err
	case <-ctx.Done():
	}

	log.Info("http server shutting down", slog.Duration("grace", grace))

	// ctx is already canceled, so its cancellation is detached first.
	// Otherwise Shutdown gives up immediately and cuts off the requests that
	// are still running.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), grace)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	return <-serveErr
}
