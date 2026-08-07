package redis_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ragbuaj/project-management/backend/internal/redis"
)

// keyPrefix is unique to the calling test and to this run, so that parallel
// tests cannot count each other's hits and a rerun cannot count the previous
// run's.
//
// The run part matters more than it looks. The longest window here is a minute,
// and Redis outlives the process, so without it `go test -count=2` and any CI
// rerun inside that minute start with the counters already at their limit —
// which reads as a broken limiter rather than a test reusing a key.
func keyPrefix(t *testing.T) string {
	t.Helper()

	return fmt.Sprintf("%s:%d:", t.Name(), time.Now().UnixNano())
}

// limiter builds a limiter against the real Redis.
func limiter(t *testing.T, limit int, window time.Duration) (*redis.SlidingWindow, string) {
	t.Helper()

	client, err := redis.New(testRedisURL(t), 5)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return redis.NewSlidingWindow(client, limit, window), keyPrefix(t)
}

func TestAllowCountsUpToTheLimitAndThenRefuses(t *testing.T) {
	t.Parallel()

	lim, prefix := limiter(t, 3, time.Minute)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	for i := 1; i <= 3; i++ {
		allowed, _, err := lim.Allow(ctx, prefix+"a")
		if err != nil {
			t.Fatalf("hit %d: %v", i, err)
		}

		if !allowed {
			t.Fatalf("hit %d was refused while under the limit", i)
		}
	}

	allowed, retryAfter, err := lim.Allow(ctx, prefix+"a")
	if err != nil {
		t.Fatalf("hit 4: %v", err)
	}

	if allowed {
		t.Error("the fourth hit was allowed with a limit of three")
	}

	if retryAfter <= 0 || retryAfter > time.Minute {
		t.Errorf("retryAfter = %s, want something inside the window", retryAfter)
	}
}

// This is the whole reason the fixed window was replaced, so it is the test
// that has to fail against one.
//
// Hits age out one at a time, on their own clocks. A fixed window instead
// clears every hit at once when its window turns over, which admits the tail of
// one window plus the head of the next — twice the limit, back to back.
func TestHitsAgeOutOneAtATimeRatherThanAllAtOnce(t *testing.T) {
	t.Parallel()

	lim, prefix := limiter(t, 2, 2*time.Second)

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	key := prefix + "a"

	if allowed, _, err := lim.Allow(ctx, key); err != nil || !allowed {
		t.Fatalf("hit at t=0: allowed=%v err=%v", allowed, err)
	}

	time.Sleep(1200 * time.Millisecond)

	if allowed, _, err := lim.Allow(ctx, key); err != nil || !allowed {
		t.Fatalf("hit at t=1.2s: allowed=%v err=%v", allowed, err)
	}

	if allowed, _, _ := lim.Allow(ctx, key); allowed {
		t.Fatal("a third hit was allowed with two already inside the window")
	}

	// t=2.2s. The first hit expired at t=2s; the second does not until t=3.2s.
	time.Sleep(time.Second)

	allowed, _, err := lim.Allow(ctx, key)
	if err != nil {
		t.Fatalf("hit at t=2.2s: %v", err)
	}

	if !allowed {
		t.Error("the oldest hit never aged out; the window is not sliding")
	}

	// The hit from t=1.2s is still inside the window, so this one is over the
	// limit. A fixed window would have cleared it along with the first and let
	// this through — the doubling ADR-0010 refuses on the login path.
	if allowed, _, _ := lim.Allow(ctx, key); allowed {
		t.Error("two hits were admitted after one aged out; the window turned over wholesale")
	}
}

// A refused hit must not be remembered. If it were, every attempt would push
// the newest hit forward and a caller under sustained load would never drain
// below the limit — a block that waiting does not resolve.
func TestRefusedHitsDoNotPushTheWindowForward(t *testing.T) {
	t.Parallel()

	lim, prefix := limiter(t, 1, time.Second)

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	key := prefix + "a"

	if allowed, _, err := lim.Allow(ctx, key); err != nil || !allowed {
		t.Fatalf("first hit: allowed=%v err=%v", allowed, err)
	}

	// Keep hammering while refused, the last of them at roughly t=0.8s.
	for range 4 {
		time.Sleep(200 * time.Millisecond)

		if allowed, _, _ := lim.Allow(ctx, key); allowed {
			t.Fatal("a hit was allowed inside the window")
		}
	}

	// t=1.1s. Only the admitted hit at t=0 was ever recorded, and it has expired.
	time.Sleep(300 * time.Millisecond)

	allowed, _, err := lim.Allow(ctx, key)
	if err != nil {
		t.Fatalf("after the window: %v", err)
	}

	if !allowed {
		t.Error("still refused after the window; refused hits are being recorded")
	}
}

// layered builds a limiter with several tiers, on a key prefix unique to the
// calling test.
func layered(t *testing.T, tiers ...redis.Tier) (*redis.SlidingWindow, string) {
	t.Helper()

	client, err := redis.New(testRedisURL(t), 5)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return redis.NewLayered(client, tiers...), keyPrefix(t)
}

// Every tier is enforced, not just the one that happens to be checked first.
// ADR-0010 pairs a tight window with a loose one precisely so that a guesser
// who waits between rounds still runs into the loose one.
func TestEveryTierIsEnforced(t *testing.T) {
	t.Parallel()

	lim, prefix := layered(t,
		redis.Tier{Limit: 2, Window: 500 * time.Millisecond},
		redis.Tier{Limit: 3, Window: time.Minute},
	)

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	key := prefix + "a"

	for i := 1; i <= 2; i++ {
		if allowed, _, err := lim.Allow(ctx, key); err != nil || !allowed {
			t.Fatalf("hit %d: allowed=%v err=%v", i, allowed, err)
		}
	}

	if allowed, _, _ := lim.Allow(ctx, key); allowed {
		t.Fatal("a third hit was allowed while the tight tier was full")
	}

	// The tight tier drains; the minute-long one does not.
	time.Sleep(600 * time.Millisecond)

	if allowed, _, err := lim.Allow(ctx, key); err != nil || !allowed {
		t.Fatalf("hit after the tight tier drained: allowed=%v err=%v", allowed, err)
	}

	// Three admitted hits now sit in the loose tier, which is its whole limit,
	// while the tight tier holds one of two. Only the loose tier can refuse
	// this, so a limiter that checked the tight one alone would let it through.
	allowed, retryAfter, err := lim.Allow(ctx, key)
	if err != nil {
		t.Fatalf("hit over the loose tier: %v", err)
	}

	if allowed {
		t.Error("the loose tier never refused; only the tight tier is being enforced")
	}

	if retryAfter <= time.Second {
		t.Errorf("retryAfter = %s, want the loose tier's remaining time", retryAfter)
	}
}

// A hit refused by one tier must not be recorded by any of them. Recording it
// in the tiers that still had room would spend a budget the caller never got to
// use, and under sustained load those tiers would never drain.
func TestARefusedHitIsRecordedNowhere(t *testing.T) {
	t.Parallel()

	lim, prefix := layered(t,
		redis.Tier{Limit: 1, Window: 300 * time.Millisecond},
		redis.Tier{Limit: 5, Window: time.Minute},
	)

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	key := prefix + "a"

	if allowed, _, err := lim.Allow(ctx, key); err != nil || !allowed {
		t.Fatalf("first hit: allowed=%v err=%v", allowed, err)
	}

	// Five refusals from the tight tier. Counted in the loose tier they would
	// take it to six, past its limit of five.
	for i := range 5 {
		if allowed, _, _ := lim.Allow(ctx, key); allowed {
			t.Fatalf("refused hit %d was allowed inside the tight window", i+1)
		}
	}

	time.Sleep(400 * time.Millisecond)

	allowed, _, err := lim.Allow(ctx, key)
	if err != nil {
		t.Fatalf("after the tight tier drained: %v", err)
	}

	if !allowed {
		t.Error("the loose tier is full; refusals are being recorded in tiers that admitted them")
	}
}

// A limiter with nothing configured admits everything. It is what an empty tier
// list means, and saying so here is cheaper than a nil dereference on the first
// request of a path somebody forgot to configure.
func TestALimiterWithNoTiersAdmitsEverything(t *testing.T) {
	t.Parallel()

	lim, prefix := layered(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	for i := range 3 {
		allowed, _, err := lim.Allow(ctx, prefix+"a")
		if err != nil {
			t.Fatalf("hit %d: %v", i+1, err)
		}

		if !allowed {
			t.Fatalf("hit %d was refused by a limiter with no limits", i+1)
		}
	}
}

// The limit is per key. One account being locked out must not lock out
// everyone else, which is what a shared counter would do.
func TestKeysAreCountedSeparately(t *testing.T) {
	t.Parallel()

	lim, prefix := limiter(t, 1, time.Minute)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	for _, key := range []string{prefix + "first", prefix + "second"} {
		allowed, _, err := lim.Allow(ctx, key)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}

		if !allowed {
			t.Errorf("the first hit on %s was refused", key)
		}
	}

	if allowed, _, _ := lim.Allow(ctx, prefix+"first"); allowed {
		t.Error("the second hit on the first key was allowed")
	}
}

// The record has to clear by itself. Without the expiry the key lives forever,
// and every hit ever counted stays in the sorted set — memory that only grows,
// on a key nobody will ever look at again.
func TestTheWindowExpires(t *testing.T) {
	t.Parallel()

	lim, prefix := limiter(t, 1, 300*time.Millisecond)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	if allowed, _, err := lim.Allow(ctx, prefix+"a"); err != nil || !allowed {
		t.Fatalf("first hit: allowed=%v err=%v", allowed, err)
	}

	if allowed, _, _ := lim.Allow(ctx, prefix+"a"); allowed {
		t.Fatal("the second hit was allowed inside the window")
	}

	time.Sleep(400 * time.Millisecond)

	allowed, _, err := lim.Allow(ctx, prefix+"a")
	if err != nil {
		t.Fatalf("after the window: %v", err)
	}

	if !allowed {
		t.Error("the window never expired; the key is counting forever")
	}
}

// Two requests arriving together must not both read the same count and both be
// allowed. This is what the script buys, and it is also what proves hits landing
// in the same millisecond are counted as two rather than collapsing into one
// sorted-set member.
func TestConcurrentHitsAreCountedExactly(t *testing.T) {
	t.Parallel()

	const (
		limit   = 5
		callers = 40
	)

	lim, prefix := limiter(t, limit, time.Minute)

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		granted int
	)

	for range callers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			allowed, _, err := lim.Allow(ctx, prefix+"a")
			if err != nil {
				t.Errorf("Allow(): %v", err)

				return
			}

			if allowed {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	if granted != limit {
		t.Errorf("%d of %d concurrent callers were allowed, want exactly %d", granted, callers, limit)
	}
}

// An error must never be reported as allowed. The middleware decides what a
// failure means per path, and it can only do that if a failure arrives as one.
func TestADeadRedisIsAnErrorAndNotAVerdict(t *testing.T) {
	t.Parallel()

	client, err := redis.New("redis://127.0.0.1:1", 5)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	allowed, _, err := redis.NewSlidingWindow(client, 5, time.Minute).Allow(ctx, "anything")
	if err == nil {
		t.Fatal("Allow() against a dead Redis returned no error")
	}

	if allowed {
		t.Error("Allow() reported allowed while it could not count anything")
	}
}

// Retry-After is derived from this, so it has to shrink as the oldest hit ages
// rather than restart at the full window on every refused attempt.
func TestRetryAfterCountsDownWithTheWindow(t *testing.T) {
	t.Parallel()

	lim, prefix := limiter(t, 1, 2*time.Second)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	if _, _, err := lim.Allow(ctx, prefix+"a"); err != nil {
		t.Fatalf("first hit: %v", err)
	}

	_, first, err := lim.Allow(ctx, prefix+"a")
	if err != nil {
		t.Fatalf("second hit: %v", err)
	}

	time.Sleep(700 * time.Millisecond)

	_, later, err := lim.Allow(ctx, prefix+"a")
	if err != nil {
		t.Fatalf("third hit: %v", err)
	}

	if later >= first {
		t.Errorf("retryAfter went from %s to %s; it is not counting down", first, later)
	}
}

// Keys are prefixed so that clearing the cache or the pub/sub bookkeeping in
// the same database cannot reset every rate limit as a side effect. The two
// prefixes are separate rather than one plus a suffix, so that a caller's key
// can never be mistaken for the sequence counter of a shorter one.
func TestKeysAreNamespaced(t *testing.T) {
	t.Parallel()

	client, err := redis.New(testRedisURL(t), 5)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	key := fmt.Sprintf("%s:namespaced", t.Name())

	if _, _, err := redis.NewSlidingWindow(client, 5, time.Minute).Allow(ctx, key); err != nil {
		t.Fatalf("Allow(): %v", err)
	}

	// The window length is part of the name so that two tiers of the same
	// limiter do not share one counter, and it sits in front of the caller's
	// key rather than behind it for the same reason as the prefixes.
	const window = "60000"

	hits, seq := "ratelimit:hits:"+window+":"+key, "ratelimit:seq:"+window+":"+key

	t.Cleanup(func() { client.Del(context.Background(), hits, seq) })

	for _, name := range []string{hits, seq} {
		n, err := client.Exists(ctx, name).Result()
		if err != nil {
			t.Fatalf("Exists(%q): %v", name, err)
		}

		if n != 1 {
			t.Errorf("no key at %s; the namespace changed", name)
		}
	}
}
