package redis

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Tier is one limit over one window. ADR-0010 gives most buckets two of them —
// five failures a quarter hour to slow a guesser down, twenty an hour to stop
// one that waits between rounds — because a single window can only be tuned for
// one of those two and is wrong for the other.
type Tier struct {
	Limit  int
	Window time.Duration
}

// admitOne drops everything that has aged out of every tier, admits the hit if
// all of them have room, and answers how long the caller must wait if any does
// not.
//
// It is a script because those steps have to be one atomic operation. Read the
// counts in one round trip and write in another and two callers arriving
// together both read the same counts and both find room, which is the whole
// failure a rate limit exists to prevent. The tiers are in the same script for
// the same reason one step further out: checking them one call at a time would
// let a hit land in the tier that had room after a later tier had already
// refused it.
//
// Nothing is recorded unless every tier admits the hit. A refused hit that
// still counted somewhere would push that tier's window forward on every
// attempt, so a caller under sustained load would never drain below the limit
// and the block would become permanent — a lockout that no amount of waiting
// resolves.
//
// The clock is Redis's own (TIME) rather than the caller's. Scores are compared
// across every instance of this application, so a machine whose clock runs a
// minute fast would otherwise write hits that no other instance can age out for
// a minute. Redis 5 and later replicate scripts by their effects, so a
// non-deterministic command here is safe.
var admitOne = redis.NewScript(`
	local clock = redis.call('TIME')
	local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)

	local tiers = #ARGV / 2
	local wait = 0

	for i = 1, tiers do
		local hits   = KEYS[i * 2 - 1]
		local window = tonumber(ARGV[i * 2 - 1])
		local limit  = tonumber(ARGV[i * 2])

		redis.call('ZREMRANGEBYSCORE', hits, '-inf', now - window)

		if redis.call('ZCARD', hits) >= limit then
			local oldest = redis.call('ZRANGE', hits, 0, 0, 'WITHSCORES')
			local retry = window

			if oldest[2] ~= nil then
				retry = tonumber(oldest[2]) + window - now
			end

			-- The caller is told the longest of the waits, not the first one
			-- found. A shorter answer would invite a retry that is still
			-- refused, which reads from the outside as the limit lying.
			if retry > wait then
				wait = retry
			end
		end
	end

	if wait > 0 then
		return {0, wait}
	end

	for i = 1, tiers do
		local hits   = KEYS[i * 2 - 1]
		local seq    = KEYS[i * 2]
		local window = tonumber(ARGV[i * 2 - 1])

		-- Two hits in the same millisecond need distinct members, or the second
		-- ZADD updates the first instead of adding to it and the pair counts
		-- once.
		local n = redis.call('INCR', seq)

		redis.call('ZADD', hits, now, now .. '-' .. n)
		redis.call('PEXPIRE', hits, window)
		redis.call('PEXPIRE', seq, window)
	end

	return {1, 0}
`)

// SlidingWindow counts hits per key over windows that move with the clock.
//
// Each hit is remembered individually and ages out on its own, so a limit holds
// across every instant rather than only within a window the server picked. The
// fixed window this replaces admitted up to twice the limit around a boundary —
// the tail of one window plus the head of the next — which ADR-0010 rejects for
// the authentication path.
//
// The cost is one sorted set entry per admitted hit per tier. The limits in
// ADR-0010 top out in the hundreds per key per day, so a key holds hundreds of
// small members at worst, and it expires on its own once the hits stop.
type SlidingWindow struct {
	client *Client
	tiers  []Tier
}

// NewSlidingWindow builds a limiter admitting limit hits per key per window.
//
// A limit below one refuses everything, which is a way to switch a path off but
// is far more often a bug in the caller. It is not rejected here because there
// is nowhere to report it; callers build these at start-up from constants.
func NewSlidingWindow(client *Client, limit int, window time.Duration) *SlidingWindow {
	return NewLayered(client, Tier{Limit: limit, Window: window})
}

// NewLayered builds a limiter that admits a hit only when every tier has room.
//
// Tiers are told apart by their window length, so reordering them keeps each
// one counting against what it was already counting against. Two tiers sharing
// a window would therefore share a counter; that is a configuration with no
// meaning — the smaller limit already decides — and it stays harmless rather
// than becoming an error nobody can act on at start-up.
//
// With no tiers at all the limiter admits everything, which is what an empty
// list means and is easier to see here than a panic on the first request.
func NewLayered(client *Client, tiers ...Tier) *SlidingWindow {
	return &SlidingWindow{client: client, tiers: tiers}
}

// Allow counts one hit against key and reports whether it is under every limit.
//
// retryAfter is how long the caller should wait, and is only meaningful when
// allowed is false. An error means Redis did not answer; it is never reported
// as either allowed or denied, because that choice belongs to the caller —
// docs/nfr.md wants the authentication path to fail closed and everything else
// to keep serving.
func (s *SlidingWindow) Allow(ctx context.Context, key string) (allowed bool, retryAfter time.Duration, err error) {
	if len(s.tiers) == 0 {
		return true, 0, nil
	}

	keys := make([]string, 0, len(s.tiers)*2)
	argv := make([]any, 0, len(s.tiers)*2)

	for _, t := range s.tiers {
		window := t.Window.Milliseconds()

		keys = append(keys, hitsKey(window, key), sequenceKey(window, key))
		argv = append(argv, window, int64(t.Limit))
	}

	res, err := admitOne.Run(ctx, s.client, keys, argv...).Int64Slice()
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
		return false, s.longestWindow(), nil
	}

	return false, time.Duration(res[1]) * time.Millisecond, nil
}

func (s *SlidingWindow) longestWindow() time.Duration {
	var longest time.Duration

	for _, t := range s.tiers {
		longest = max(longest, t.Window)
	}

	return longest
}

// The two prefixes keep these keys apart from cache and pub/sub in the same
// database, so that clearing one kind never resets the others. They also keep
// the sequence counter out of the sorted set's own namespace, and each tier out
// of the others'. Every discriminator goes in front of the caller's key rather
// than after it: a suffix would collide with a caller whose key happens to end
// in it, and callers build keys out of addresses and identifiers this package
// never sees.
func hitsKey(window int64, key string) string {
	return "ratelimit:hits:" + strconv.FormatInt(window, 10) + ":" + key
}

func sequenceKey(window int64, key string) string {
	return "ratelimit:seq:" + strconv.FormatInt(window, 10) + ":" + key
}
