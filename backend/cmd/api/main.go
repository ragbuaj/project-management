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
	"time"

	"github.com/ragbuaj/project-management/backend/internal/config"
	"github.com/ragbuaj/project-management/backend/internal/httpx"
	identityhttp "github.com/ragbuaj/project-management/backend/internal/modules/identity/handler"
	identityrepo "github.com/ragbuaj/project-management/backend/internal/modules/identity/repository"
	identityroute "github.com/ragbuaj/project-management/backend/internal/modules/identity/route"
	identitysvc "github.com/ragbuaj/project-management/backend/internal/modules/identity/service"
	"github.com/ragbuaj/project-management/backend/internal/postgres"
	"github.com/ragbuaj/project-management/backend/internal/redis"
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

	// The opposite of the pool above: docs/nfr.md states the application keeps
	// running when Redis is down, so an unreachable Redis must not stop
	// start-up. Only a malformed URL does, and that is a configuration
	// mistake rather than an outage.
	// Before the first client exists, so nothing the library reports escapes
	// as unparseable plain text on stderr.
	redis.UseLogger(log)

	rdb, err := redis.New(cfg.RedisURL, config.RedisPoolSize)
	if err != nil {
		return fmt.Errorf("redis: %w", err)
	}

	defer func() { _ = rdb.Close() }()

	// Reported once at start-up so the state is in the log rather than
	// inferred later from whatever started behaving oddly. It changes nothing
	// about whether the process runs.
	pingCtx, cancelPing := context.WithTimeout(ctx, config.ReadyTimeout)
	defer cancelPing()

	if err := redis.Ping(pingCtx, rdb); err != nil {
		log.Warn("redis is not answering; realtime and caching are degraded and the login rate limit fails closed",
			slog.String("error", err.Error()))
	}

	// One Queries bound to the pool. A service that needs a transaction takes
	// one from the pool and builds its own; nothing here needs that yet.
	queries := identityrepo.New(pool)

	credentials, err := identitysvc.NewCredentials(queries, log)
	if err != nil {
		return fmt.Errorf("identity credentials: %w", err)
	}

	sessions := identitysvc.NewSessions(queries, log, time.Now)
	guard := loginGuard(rdb, log)

	mux := http.NewServeMux()
	identityroute.Register(mux,
		identityhttp.NewAuth(credentials, sessions, guard, cfg.TrustedProxies, log),
		sessions, log)

	mux.Handle("GET /healthz", httpx.Health())
	mux.Handle("GET /readyz", httpx.Ready(log, config.ReadyTimeout, httpx.ReadyCheck{
		Name:  "postgres",
		Probe: pool.Ping,
	}))

	handler := apiHandler(mux, log)

	var lc net.ListenConfig

	ln, err := lc.Listen(ctx, "tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.HTTPAddr, err)
	}

	srv := httpx.NewServer(handler, httpx.Timeouts{
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

// loginGuard builds the three failure counters ADR-0010 asks for.
//
// The numbers live in the identity module, next to the code that enforces them;
// this only translates them into the shape the Redis counter takes. The two
// packages do not know about each other by design (ADR-0008), and one small
// conversion here is the price of that.
func loginGuard(rdb *redis.Client, log *slog.Logger) *identitysvc.LoginGuard {
	limits := identitysvc.DefaultLoginLimits()

	counter := func(buckets []identitysvc.Bucket) *redis.FailureCounter {
		tiers := make([]redis.Tier, 0, len(buckets))
		for _, b := range buckets {
			tiers = append(tiers, redis.Tier{Limit: b.Limit, Window: b.Window})
		}

		return redis.NewFailureCounter(rdb, tiers...)
	}

	return identitysvc.NewLoginGuard(
		counter(limits.Account),
		counter(limits.Address),
		counter(limits.Network),
		log,
	)
}

// apiHandler wraps mux in the middleware every request passes through.
//
// It is a function rather than four lines inside run because ADR-0005 puts the
// CSRF check at the router precisely so that no route can be added without it,
// and a chain that is only assembled inside run cannot be asked whether that
// is still true.
//
// Order is not interchangeable.
//
// RequestID is outermost, so everything below it can name the request. Recover
// sits inside LogRequests rather than outside: a panic unwinds through every
// frame above it, so a Recover placed outside would take the panic before
// LogRequests could write its line, and the request would vanish from the log
// it is supposed to appear in. This way Recover turns the panic into a 500,
// returns normally, and LogRequests records it as what it became.
//
// CSRF is innermost, so a refusal is still logged and still carries an id.
func apiHandler(mux http.Handler, log *slog.Logger) http.Handler {
	return httpx.Chain(mux,
		httpx.RequestID,
		httpx.LogRequests(log),
		httpx.Recover(log),
		httpx.CSRF(log),
	)
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
