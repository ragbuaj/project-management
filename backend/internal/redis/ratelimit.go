package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// incrementAndExpire counts one hit and returns the count together with the
// time left in the window.
//
// It is a script because the two commands have to be one step. INCR followed by
// a separate PEXPIRE leaves a window where the first succeeded and the second
// did not — and the key then has no expiry at all, so the counter never resets
// and the caller is locked out forever. That failure is rare, permanent, and
// impossible to explain from the outside.
var incrementAndExpire = redis.NewScript(`
	local hits = redis.call('INCR', KEYS[1])
	if hits == 1 then
		redis.call('PEXPIRE', KEYS[1], ARGV[1])
	end
	return {hits, redis.call('PTTL', KEYS[1])}
`)

// FixedWindow counts hits per key in windows of a fixed length.
//
// A fixed window admits up to twice the limit across a window boundary — the
// tail of one window plus the head of the next. For the limits this guards,
// measured in single digits per quarter hour, that is the difference between
// five attempts and ten, which changes nothing about whether a password can be
// guessed. A sliding window would cost a sorted set and a range delete per
// request to close a gap that does not matter here.
type FixedWindow struct {
	client *Client
	limit  int64
	window time.Duration
}

// NewFixedWindow builds a limiter allowing limit hits per key per window.
func NewFixedWindow(client *Client, limit int, window time.Duration) *FixedWindow {
	return &FixedWindow{client: client, limit: int64(limit), window: window}
}

// Allow counts one hit against key and reports whether it is under the limit.
//
// retryAfter is how long the caller should wait, and is only meaningful when
// allowed is false. An error means Redis did not answer; it is never reported
// as either allowed or denied, because that choice belongs to the caller —
// docs/nfr.md wants the authentication path to fail closed and everything else
// to keep serving.
func (f *FixedWindow) Allow(ctx context.Context, key string) (allowed bool, retryAfter time.Duration, err error) {
	// The prefix keeps these keys apart from cache and pub/sub in the same
	// database, so that flushing one kind never takes the others with it.
	res, err := incrementAndExpire.Run(ctx, f.client,
		[]string{"ratelimit:" + key},
		f.window.Milliseconds(),
	).Int64Slice()
	if err != nil {
		return false, 0, fmt.Errorf("rate limit %q: %w", key, err)
	}

	if len(res) != 2 {
		return false, 0, fmt.Errorf("rate limit %q: script returned %d values, want 2", key, len(res))
	}

	hits, ttlMillis := res[0], res[1]

	if hits <= f.limit {
		return true, 0, nil
	}

	// PTTL answers -1 when the key somehow has no expiry and -2 when it is
	// already gone. Neither should happen given the script above, but a
	// negative duration would become a Retry-After in the past and tell the
	// caller to try again immediately.
	if ttlMillis < 0 {
		return false, f.window, nil
	}

	return false, time.Duration(ttlMillis) * time.Millisecond, nil
}
