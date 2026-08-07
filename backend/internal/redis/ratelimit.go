package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// admitOne drops everything that has aged out of the window, admits the hit if
// there is room, and answers how long the caller must wait if there is not.
//
// It is a script because those steps have to be one atomic operation. Read the
// count in one round trip and write in another and two callers arriving
// together both read the same count and both find room, which is the whole
// failure a rate limit exists to prevent.
//
// The clock is Redis's own (TIME) rather than the caller's. Scores are compared
// across every instance of this application, so a machine whose clock runs a
// minute fast would otherwise write hits that no other instance can age out for
// a minute. Redis 5 and later replicate scripts by their effects, so a
// non-deterministic command here is safe.
//
// A refused hit is deliberately not recorded. Recording it would push the
// window forward on every attempt, so a caller under sustained load would never
// drain below the limit and the block would become permanent — a lockout that
// no amount of waiting resolves.
var admitOne = redis.NewScript(`
	local window = tonumber(ARGV[1])
	local limit  = tonumber(ARGV[2])

	local clock = redis.call('TIME')
	local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)

	redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now - window)

	if redis.call('ZCARD', KEYS[1]) >= limit then
		local oldest = redis.call('ZRANGE', KEYS[1], 0, 0, 'WITHSCORES')
		if oldest[2] == nil then
			return {0, window}
		end

		return {0, tonumber(oldest[2]) + window - now}
	end

	-- Two hits in the same millisecond need distinct members, or the second
	-- ZADD updates the first instead of adding to it and the pair counts once.
	local seq = redis.call('INCR', KEYS[2])

	redis.call('ZADD', KEYS[1], now, now .. '-' .. seq)
	redis.call('PEXPIRE', KEYS[1], window)
	redis.call('PEXPIRE', KEYS[2], window)

	return {1, 0}
`)

// SlidingWindow counts hits per key over a window that moves with the clock.
//
// Each hit is remembered individually and ages out on its own, so the limit
// holds across every instant rather than only within a window the server picked.
// The fixed window this replaces admitted up to twice the limit around a
// boundary — the tail of one window plus the head of the next — which ADR-0010
// rejects for the authentication path.
//
// The cost is one sorted set entry per admitted hit. The limits in ADR-0010 top
// out in the hundreds per key per day, so a key holds hundreds of small members
// at worst, and it expires on its own once the hits stop.
type SlidingWindow struct {
	client *Client
	limit  int64
	window time.Duration
}

// NewSlidingWindow builds a limiter admitting limit hits per key per window.
//
// A limit below one refuses everything, which is a way to switch a path off but
// is far more often a bug in the caller. It is not rejected here because there
// is nowhere to report it; callers build these at start-up from constants.
func NewSlidingWindow(client *Client, limit int, window time.Duration) *SlidingWindow {
	return &SlidingWindow{client: client, limit: int64(limit), window: window}
}

// Allow counts one hit against key and reports whether it is under the limit.
//
// retryAfter is how long the caller should wait, and is only meaningful when
// allowed is false. An error means Redis did not answer; it is never reported
// as either allowed or denied, because that choice belongs to the caller —
// docs/nfr.md wants the authentication path to fail closed and everything else
// to keep serving.
func (s *SlidingWindow) Allow(ctx context.Context, key string) (allowed bool, retryAfter time.Duration, err error) {
	res, err := admitOne.Run(ctx, s.client,
		[]string{hitsKey(key), sequenceKey(key)},
		s.window.Milliseconds(), s.limit,
	).Int64Slice()
	if err != nil {
		return false, 0, fmt.Errorf("rate limit %q: %w", key, err)
	}

	if len(res) != 2 {
		return false, 0, fmt.Errorf("rate limit %q: script returned %d values, want 2", key, len(res))
	}

	if res[0] == 1 {
		return true, 0, nil
	}

	// Guarding the sign rather than trusting it: a Retry-After in the past reads
	// as "try again now", which turns a refusal into a tight retry loop against
	// a Redis that is already the thing under strain.
	if res[1] <= 0 {
		return false, s.window, nil
	}

	return false, time.Duration(res[1]) * time.Millisecond, nil
}

// The two prefixes keep these keys apart from cache and pub/sub in the same
// database, so that clearing one kind never resets the others. They also keep
// the sequence counter out of the sorted set's own namespace: a suffix would
// collide with a caller whose key happens to end in it, and callers build keys
// out of addresses and identifiers this package never sees.
func hitsKey(key string) string     { return "ratelimit:hits:" + key }
func sequenceKey(key string) string { return "ratelimit:seq:" + key }
