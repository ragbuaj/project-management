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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ragbuaj/project-management/backend/internal/config"
	"github.com/ragbuaj/project-management/backend/internal/httpx"
	"github.com/ragbuaj/project-management/backend/internal/mail"
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

	// Contacts nothing: a mail server that is down must not stop this
	// application from starting, because every part of it that is not mail
	// still works.
	sender, err := mail.NewSMTP(mail.Options{
		Host:     cfg.SMTP.Host,
		Port:     cfg.SMTP.Port,
		From:     cfg.SMTP.From,
		Username: cfg.SMTP.Username,
		Password: cfg.SMTP.Password,
		// Mailpit speaks neither STARTTLS nor AUTH. Tied to APP_ENV rather than
		// to a variable of its own, so that no separate switch can be set wrong
		// in production — the same reason the Secure cookie attribute hangs off
		// it.
		AllowUnencrypted: cfg.Env == config.EnvLocal,
	})
	if err != nil {
		return fmt.Errorf("smtp: %w", err)
	}

	invitations := identitysvc.NewInvitations(inTx(pool), sender, cfg.BaseURL, log, time.Now)
	resets := identitysvc.NewPasswordResets(inTx(pool), sender, resetAccountLimit(rdb), cfg.BaseURL, log, time.Now)

	mux := http.NewServeMux()
	identityroute.Register(mux,
		identityhttp.NewAuth(credentials, sessions, guard, cfg.TrustedProxies, log),
		identityhttp.NewInvitations(invitations, sessions, log),
		identityhttp.NewPasswordResets(resets, log),
		sessions,
		inviteLimit(rdb, log),
		acceptLimit(rdb, cfg.TrustedProxies, log),
		resetRequestLimit(rdb, cfg.TrustedProxies, log),
		resetConfirmLimit(rdb, cfg.TrustedProxies, log),
		log)

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

// inTx gives the identity services their transaction boundary.
//
// The boundary belongs to the service layer (ADR-0008), but pgx does not: the
// service takes a function, which is what keeps it testable with an ordinary
// fake instead of something that has to impersonate pgx.Tx. Opening the
// transaction is this file's job because this file is where the pool lives.
func inTx(pool *pgxpool.Pool) identitysvc.InTx {
	return func(ctx context.Context, fn func(identitysvc.TxStore) error) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin: %w", err)
		}

		// Runs after a successful commit too, where it is a no-op. It is the
		// only thing that releases the connection when fn panics.
		defer func() { _ = tx.Rollback(ctx) }()

		if err := fn(identityrepo.New(tx)); err != nil {
			return err
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit: %w", err)
		}

		return nil
	}
}

// inviteLimit is the rate limit ADR-0005 §131 asks for on invitations.
//
// Keyed by the caller's account rather than by address: only the owner reaches
// this endpoint, so an address bucket would be one bucket for one person and a
// second name for the same count. It fails **open** — docs/nfr.md keeps
// fail-closed for the authentication endpoints, where turning the guard off is
// worse than refusing for a while, and an owner who cannot invite anybody
// because Redis is down is the opposite trade.
//
// A request with no session keys nothing and is not counted; the guard behind
// this refuses it anyway.
func inviteLimit(rdb *redis.Client, log *slog.Logger) httpx.Middleware {
	limiter := redis.NewLayered(rdb,
		redis.Tier{Limit: 20, Window: time.Hour},
		redis.Tier{Limit: 100, Window: 24 * time.Hour},
	)

	key := func(r *http.Request) string {
		who, ok := identityhttp.CallerFrom(r.Context())
		if !ok {
			return ""
		}

		return "invite:" + who.UserID
	}

	return httpx.RateLimit(limiter, key, httpx.FailOpen, log)
}

// acceptLimit is the rate limit on redeeming an invitation link.
//
// Keyed by the caller's address, because there is no account yet — that is the
// whole point of the endpoint. It fails **closed**: this is an unauthenticated
// endpoint that reads a credential out of a URL, and docs/nfr.md keeps
// fail-closed for exactly that shape. A caller whose address cannot be worked
// out is refused for the same reason (ADR-0010).
//
// It is httpx.RateLimit rather than the login guard even though a token is
// verified here: a 32-byte random token is not something anybody guesses, so
// what is worth bounding is the traffic, not the failures.
func acceptLimit(rdb *redis.Client, trusted httpx.TrustedProxies, log *slog.Logger) httpx.Middleware {
	limiter := redis.NewLayered(rdb,
		redis.Tier{Limit: 10, Window: 10 * time.Minute},
		redis.Tier{Limit: 50, Window: time.Hour},
	)

	key := func(r *http.Request) string {
		addr, ok := httpx.ClientIP(r, trusted)
		if !ok {
			// Never empty, which would skip the limit. One shared bucket for
			// every unkeyable caller is the refusing side of a case that
			// should not occur — RemoteAddr comes from net/http, not from the
			// caller.
			return "accept:unkeyable"
		}

		return "accept:" + httpx.RateLimitKey(addr)
	}

	return httpx.RateLimit(limiter, key, httpx.FailClosed, log)
}

// resetAccountLimit is the per-account half of what ADR-0010 asks for on a
// password reset request: three an hour.
//
// It is handed to the service rather than mounted as middleware, because the
// address it is keyed by arrives in the request body and a middleware that read
// the body to find it would consume it. The service hashes the address before it
// becomes a key, so Redis never holds a list of who has been asking.
//
// One tier, not two. The layered shape exists to catch somebody who waits
// between rounds, and three an hour is already tight enough that there is
// nothing to wait out.
func resetAccountLimit(rdb *redis.Client) *redis.SlidingWindow {
	return redis.NewLayered(rdb, redis.Tier{Limit: 3, Window: time.Hour})
}

// resetRequestLimit is the per-address half: ten an hour.
//
// It fails **closed**, like acceptLimit and for the same reason — an
// unauthenticated endpoint, and this one causes mail to land in somebody else's
// inbox. A caller whose address cannot be worked out shares one bucket rather
// than skipping the limit.
func resetRequestLimit(rdb *redis.Client, trusted httpx.TrustedProxies, log *slog.Logger) httpx.Middleware {
	limiter := redis.NewLayered(rdb, redis.Tier{Limit: 10, Window: time.Hour})

	key := func(r *http.Request) string {
		addr, ok := httpx.ClientIP(r, trusted)
		if !ok {
			return "reset:unkeyable"
		}

		return "reset:" + httpx.RateLimitKey(addr)
	}

	return httpx.RateLimit(limiter, key, httpx.FailClosed, log)
}

// resetConfirmLimit bounds following a reset link, keyed by address.
//
// The same shape and the same numbers as acceptLimit, because it is the same
// situation: an unauthenticated endpoint that reads a credential out of a URL.
// It fails **closed**, and a caller whose address cannot be worked out shares
// one bucket rather than skipping the limit.
//
// httpx.RateLimit rather than the login guard even though a secret is verified
// here. ADR-0010 counts failures where somebody could plausibly be guessing; a
// 32-byte random token is not that, so what is worth bounding is the traffic.
func resetConfirmLimit(rdb *redis.Client, trusted httpx.TrustedProxies, log *slog.Logger) httpx.Middleware {
	limiter := redis.NewLayered(rdb,
		redis.Tier{Limit: 10, Window: 10 * time.Minute},
		redis.Tier{Limit: 50, Window: time.Hour},
	)

	key := func(r *http.Request) string {
		addr, ok := httpx.ClientIP(r, trusted)
		if !ok {
			return "reset-confirm:unkeyable"
		}

		return "reset-confirm:" + httpx.RateLimitKey(addr)
	}

	return httpx.RateLimit(limiter, key, httpx.FailClosed, log)
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
