// Package postgres builds the connection pool and owns everything that must be
// true of every connection: the timeouts PostgreSQL enforces on its own side,
// the pool size, and the start-up check that the database actually answers.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrInvalidURL is returned when the connection string cannot be parsed. The
// parse error itself is never surfaced: url parsing embeds its input in the
// message, and a connection string carries the password.
var ErrInvalidURL = errors.New("database url is malformed")

const (
	// Enforced by PostgreSQL rather than by client-side discipline. A single
	// forgotten timeout in application code cannot hold a connection open
	// forever, and a hanging transaction blocks VACUUM and bloats tables.
	statementTimeout         = "15s"
	idleInTransactionTimeout = "30s"

	// Recycling connections keeps a long-lived pool from accumulating
	// server-side state, and lets a restarted database be picked up without
	// restarting the application.
	maxConnLifetime = time.Hour
	maxConnIdleTime = 30 * time.Minute

	connectTimeout = 10 * time.Second
)

// New builds the pool and verifies the database answers before returning.
//
// Failing here stops the process at start-up, which is deliberate: docs/nfr.md
// states the application is down when PostgreSQL is down, so there is nothing
// useful to serve without it. Redis is the opposite case and is handled
// differently — see httpx.ReadyCheck.
func New(ctx context.Context, databaseURL string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, ErrInvalidURL
	}

	cfg.MaxConns = maxConns
	cfg.MaxConnLifetime = maxConnLifetime
	cfg.MaxConnIdleTime = maxConnIdleTime

	// Applied per session, so they hold for every query on every connection
	// including the ones nobody remembered to bound.
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = statementTimeout
	cfg.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = idleInTransactionTimeout

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()

		return nil, fmt.Errorf("ping: %w", err)
	}

	return pool, nil
}
