// Package redis builds the Redis client and owns everything that must be true
// of every command it sends: the timeouts, the pool size, and the decision not
// to refuse start-up when Redis is unreachable.
package redis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrInvalidURL is returned when the connection string cannot be parsed. The
// parse error itself is never surfaced: url parsing embeds its input in the
// message, and a connection string carries the password.
var ErrInvalidURL = errors.New("redis url is malformed")

const (
	// Short on purpose. Everything this client is used for — rate-limit
	// counters, cache reads, pub/sub fan-out — has a defined answer for "Redis
	// did not reply", and waiting is never it. A slow Redis must not turn into
	// a slow API.
	dialTimeout  = 2 * time.Second
	readTimeout  = 500 * time.Millisecond
	writeTimeout = 500 * time.Millisecond

	// go-redis retries a failed command by default. Left on, a Redis that is
	// down costs three timeouts per command instead of one, which turns a
	// degraded dependency into a slow one. The caller decides what a failure
	// means; it should learn about it quickly.
	maxRetries = -1

	poolTimeout = time.Second
)

// Client is the subset of go-redis this application uses. It is named here so
// callers depend on the operations rather than on the library.
type Client = redis.Client

// New builds the client and returns it without contacting Redis.
//
// Unlike postgres.New this does not verify the server answers, and that is the
// whole difference between the two dependencies. docs/nfr.md states the
// application keeps running when Redis is down: realtime stops, charts are
// recomputed, and rate limiting on the authentication path fails closed.
// Refusing to start would turn a degraded feature set into an outage.
//
// The same reasoning keeps Redis out of /readyz — see httpx.ReadyCheck.
func New(redisURL string, poolSize int) (*Client, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, ErrInvalidURL
	}

	opts.DialTimeout = dialTimeout
	opts.ReadTimeout = readTimeout
	opts.WriteTimeout = writeTimeout
	opts.MaxRetries = maxRetries
	opts.PoolTimeout = poolTimeout
	opts.PoolSize = poolSize

	return redis.NewClient(opts), nil
}

// Ping reports whether Redis answers. Callers use it to log the state at
// start-up and to drive whatever they degrade to, never to decide whether to
// start.
func Ping(ctx context.Context, c *Client) error {
	if err := c.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("ping redis: %w", err)
	}

	return nil
}

// slogAdapter sends go-redis's own diagnostics through the application logger.
type slogAdapter struct{ log *slog.Logger }

func (a slogAdapter) Printf(ctx context.Context, format string, v ...any) {
	a.log.WarnContext(ctx, fmt.Sprintf(format, v...), slog.String("component", "redis"))
}

// UseLogger routes go-redis's internal diagnostics through log.
//
// By default the library writes them to stderr as plain text. In production
// this process emits JSON, so those lines would be the only ones a log
// collector cannot parse — and they appear precisely when Redis is failing,
// which is when the log matters most.
//
// The library keeps its logger in a package-level variable, so this is called
// once during start-up and never again.
func UseLogger(log *slog.Logger) {
	redis.SetLogger(slogAdapter{log: log})
}
