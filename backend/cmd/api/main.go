// Command api runs the HTTP server that serves the API and, from PR 8 on, the
// SPA embedded into this binary.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/ragbuaj/project-management/backend/internal/config"
	"github.com/ragbuaj/project-management/backend/internal/httpx"
	"github.com/ragbuaj/project-management/backend/internal/postgres"
)

func main() {
	if err := run(); err != nil {
		// The only place the process is stopped because of an error. Written
		// to stderr rather than through the logger: if configuration failed to
		// load, there is no guarantee a logger exists yet.
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return err
	}

	log := newLogger(cfg)

	// SIGTERM is what Docker sends when a container is stopped. Without
	// handling it, every deploy cuts off the requests still in flight.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Refusing to start without the database is deliberate: docs/nfr.md states
	// the application is down when PostgreSQL is down.
	pool, err := postgres.New(ctx, cfg.DatabaseURL, cfg.DatabaseMaxConns)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}

	defer pool.Close()

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", httpx.Health())
	mux.Handle("GET /readyz", httpx.Ready(log, config.ReadyTimeout, httpx.ReadyCheck{
		Name:  "postgres",
		Probe: pool.Ping,
	}))

	var lc net.ListenConfig

	ln, err := lc.Listen(ctx, "tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.HTTPAddr, err)
	}

	srv := httpx.NewServer(mux, httpx.Timeouts{
		Read:  config.ReadTimeout,
		Write: config.WriteTimeout,
		Idle:  config.IdleTimeout,
	})

	log.Info("starting",
		slog.String("env", string(cfg.Env)),
		slog.String("base_url", cfg.BaseURL.String()),
	)

	return httpx.Serve(ctx, srv, ln, config.ShutdownTimeout, log)
}

// newLogger picks the format from the environment: readable text locally,
// structured JSON in production so it can be filtered per field.
func newLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}

	var h slog.Handler = slog.NewJSONHandler(os.Stdout, opts)
	if cfg.Env == config.EnvLocal {
		h = slog.NewTextHandler(os.Stdout, opts)
	}

	return slog.New(h)
}
