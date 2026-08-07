package redis_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ragbuaj/project-management/backend/internal/redis"
)

// limiter builds a limiter against the real Redis, on a key prefix unique to
// the calling test so parallel tests cannot count each other's hits.
func limiter(t *testing.T, limit int, window time.Duration) (*redis.FixedWindow, string) {
	t.Helper()

	client, err := redis.New(testRedisURL(t), 5)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return redis.NewFixedWindow(client, limit, window), t.Name() + ":"
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

// The counter has to reset by itself. Without the expiry the key lives
// forever, and whoever tripped the limit once is refused permanently — a
// failure that is invisible from the outside and never resolves.
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

// Two requests arriving together must not both read the same count and both
// be allowed. This is what the script buys, and it is the whole reason INCR
// and PEXPIRE are not two round trips.
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

	allowed, _, err := redis.NewFixedWindow(client, 5, time.Minute).Allow(ctx, "anything")
	if err == nil {
		t.Fatal("Allow() against a dead Redis returned no error")
	}

	if allowed {
		t.Error("Allow() reported allowed while it could not count anything")
	}
}

// Retry-After is derived from this, so it has to shrink as the window runs
// down rather than restart at the full length on every refused attempt.
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
// the same database cannot reset every rate limit as a side effect.
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

	if _, _, err := redis.NewFixedWindow(client, 5, time.Minute).Allow(ctx, key); err != nil {
		t.Fatalf("Allow(): %v", err)
	}

	t.Cleanup(func() { client.Del(context.Background(), "ratelimit:"+key) })

	n, err := client.Exists(ctx, "ratelimit:"+key).Result()
	if err != nil {
		t.Fatalf("Exists(): %v", err)
	}

	if n != 1 {
		t.Errorf("no key at ratelimit:%s; the namespace changed", key)
	}
}
