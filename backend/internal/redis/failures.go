package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// FailureCounter counts attempts that failed, and only those.
//
// It is not the same thing as SlidingWindow, and the difference is what
// ADR-0010 asks for. A rate limiter counts every request that arrives; this
// counts what went wrong, is cleared when the caller finally succeeds, and so
// measures how much guessing is going on rather than how much traffic there is.
//
// That distinction is not cosmetic on a shared address. An office behind one
// NAT — or one IPv6 /64, which this application aggregates to — produces a
// steady stream of perfectly good logins, and a limiter that counted them would
// lock the whole office out at the busiest time of the morning. ADR-0010 is
// explicit that a rate limit must not become a way to keep other people out of
// their accounts.
//
// Checking and recording are separate calls on purpose: the answer to "did this
// attempt fail" only exists after the password has been verified, which is well
// after the point where the caller must be turned away. Between those two moments
// a burst of concurrent attempts can pass a check that a later one would have
// failed. That window is bounded by how many attempts fit in the time one
// Argon2id verification takes (ADR-0005), which is the same cost that makes
// guessing expensive in the first place; closing it further would mean counting
// successes, and that is the office-lockout above.
type FailureCounter struct {
	client *Client
	tiers  []Tier
}

// NewFailureCounter builds a counter that considers a key spent when any tier is
// at its limit. With no tiers it never refuses anything.
func NewFailureCounter(client *Client, tiers ...Tier) *FailureCounter {
	return &FailureCounter{client: client, tiers: tiers}
}

// countFailures reports whether any tier is at its limit, and how long the
// oldest failure still standing in the way has left.
//
// It reads and never writes, so a check on a key nobody has failed against
// leaves nothing behind — which is every login by every honest user. Entries
// that have aged out are stepped over rather than deleted; recordFailure trims
// them, and a key nobody touches again expires whole.
var countFailures = redis.NewScript(`
	local clock = redis.call('TIME')
	local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)

	local wait = 0

	for i = 1, #KEYS do
		local window = tonumber(ARGV[i * 2 - 1])
		local limit  = tonumber(ARGV[i * 2])
		local alive  = '(' .. (now - window)

		if redis.call('ZCOUNT', KEYS[i], alive, '+inf') >= limit then
			local oldest = redis.call('ZRANGEBYSCORE', KEYS[i], alive, '+inf', 'WITHSCORES', 'LIMIT', 0, 1)
			local retry = window

			if oldest[2] ~= nil then
				retry = tonumber(oldest[2]) + window - now
			end

			-- The longest of the waits, not the first one found: a shorter
			-- answer invites a retry that is still refused.
			if retry > wait then
				wait = retry
			end
		end
	end

	if wait > 0 then
		return {0, wait}
	end

	return {1, 0}
`)

// recordFailure adds one failure to every tier.
//
// It records unconditionally rather than checking first. A caller only reaches
// this after Check let the attempt through, so the tier had room when the
// attempt began; refusing to write here would lose the very failure the check
// was standing guard for.
//
// The clock is Redis's own, for the reason given on admitOne: these scores are
// compared across every instance of this application.
var recordFailure = redis.NewScript(`
	local clock = redis.call('TIME')
	local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)

	for i = 1, #ARGV do
		local hits   = KEYS[i * 2 - 1]
		local seq    = KEYS[i * 2]
		local window = tonumber(ARGV[i])

		redis.call('ZREMRANGEBYSCORE', hits, '-inf', now - window)

		-- Two failures in the same millisecond need distinct members, or the
		-- second ZADD updates the first and the pair counts once.
		local n = redis.call('INCR', seq)

		redis.call('ZADD', hits, now, now .. '-' .. n)
		redis.call('PEXPIRE', hits, window)
		redis.call('PEXPIRE', seq, window)
	end

	return 1
`)

// Check reports whether key still has room to fail, without counting anything.
//
// retryAfter is only meaningful when ok is false. An error means Redis did not
// answer; it is never reported as either verdict, because docs/nfr.md wants the
// authentication path to fail closed and that decision belongs to the caller.
func (f *FailureCounter) Check(ctx context.Context, key string) (ok bool, retryAfter time.Duration, err error) {
	if len(f.tiers) == 0 {
		return true, 0, nil
	}

	keys := make([]string, 0, len(f.tiers))
	argv := make([]any, 0, len(f.tiers)*2)

	for _, t := range f.tiers {
		window := t.Window.Milliseconds()

		keys = append(keys, hitsKey(failureNamespace, window, key))
		argv = append(argv, window, int64(t.Limit))
	}

	res, err := countFailures.Run(ctx, f.client, keys, argv...).Int64Slice()
	if err != nil {
		return false, 0, fmt.Errorf("check failures for %q: %w", key, err)
	}

	if len(res) != 2 {
		return false, 0, fmt.Errorf("check failures for %q: script returned %d values, want 2", key, len(res))
	}

	if res[0] == 1 {
		return true, 0, nil
	}

	// A Retry-After in the past reads as "try again now", which turns a refusal
	// into a tight retry loop against a Redis that is already the thing under
	// strain.
	if res[1] <= 0 {
		return false, longestWindow(f.tiers), nil
	}

	return false, time.Duration(res[1]) * time.Millisecond, nil
}

// Record counts one failure against key in every tier.
func (f *FailureCounter) Record(ctx context.Context, key string) error {
	if len(f.tiers) == 0 {
		return nil
	}

	keys := make([]string, 0, len(f.tiers)*2)
	argv := make([]any, 0, len(f.tiers))

	for _, t := range f.tiers {
		window := t.Window.Milliseconds()

		keys = append(keys, hitsKey(failureNamespace, window, key), sequenceKey(failureNamespace, window, key))
		argv = append(argv, window)
	}

	if err := recordFailure.Run(ctx, f.client, keys, argv...).Err(); err != nil {
		return fmt.Errorf("record failure for %q: %w", key, err)
	}

	return nil
}

// Reset forgets every failure recorded against key.
//
// ADR-0010 clears the counter once the caller succeeds, so that somebody who
// mistypes a password four times and then gets it right starts the next day with
// a full allowance rather than one attempt away from being locked out.
//
// The event itself is still worth recording for anomaly detection, but that
// belongs in activity_events and not in a counter that exists to be forgotten.
func (f *FailureCounter) Reset(ctx context.Context, key string) error {
	if len(f.tiers) == 0 {
		return nil
	}

	keys := make([]string, 0, len(f.tiers)*2)

	for _, t := range f.tiers {
		window := t.Window.Milliseconds()

		keys = append(keys, hitsKey(failureNamespace, window, key), sequenceKey(failureNamespace, window, key))
	}

	if err := f.client.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("reset failures for %q: %w", key, err)
	}

	return nil
}
