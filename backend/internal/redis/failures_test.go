package redis_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ragbuaj/project-management/backend/internal/redis"
)

// counter builds a failure counter against the real Redis, on a key unique to
// the calling test and to this run.
func counter(t *testing.T, tiers ...redis.Tier) (*redis.FailureCounter, string) {
	t.Helper()

	client, err := redis.New(testRedisURL(t), 5)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return redis.NewFailureCounter(client, tiers...), keyPrefix(t) + "a"
}

// The distinction this type exists for: attempts that succeed cost nothing.
// A shared office address produces a steady stream of good logins all morning,
// and counting those would lock the whole office out.
func TestCheckingAloneNeverCountsAnything(t *testing.T) {
	t.Parallel()

	c, key := counter(t, redis.Tier{Limit: 2, Window: time.Minute})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	for i := range 20 {
		ok, _, err := c.Check(ctx, key)
		if err != nil {
			t.Fatalf("check %d: %v", i+1, err)
		}

		if !ok {
			t.Fatalf("check %d was refused, but nothing has failed yet", i+1)
		}
	}
}

func TestFailuresAccumulateUntilTheTierIsSpent(t *testing.T) {
	t.Parallel()

	c, key := counter(t, redis.Tier{Limit: 3, Window: time.Minute})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	for i := range 3 {
		if ok, _, err := c.Check(ctx, key); err != nil || !ok {
			t.Fatalf("check before failure %d: ok=%v err=%v", i+1, ok, err)
		}

		if err := c.Record(ctx, key); err != nil {
			t.Fatalf("record %d: %v", i+1, err)
		}
	}

	ok, retryAfter, err := c.Check(ctx, key)
	if err != nil {
		t.Fatalf("check after three failures: %v", err)
	}

	if ok {
		t.Error("a fourth attempt was let through after three failures with a limit of three")
	}

	if retryAfter <= 0 || retryAfter > time.Minute {
		t.Errorf("retryAfter = %s, want something inside the window", retryAfter)
	}
}

// ADR-0010 clears the counter on success, so somebody who mistypes a password
// four times and then gets it right is not left one attempt from a lockout.
func TestSuccessClearsWhatTheFailuresBuiltUp(t *testing.T) {
	t.Parallel()

	c, key := counter(t, redis.Tier{Limit: 2, Window: time.Minute})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	for i := range 2 {
		if err := c.Record(ctx, key); err != nil {
			t.Fatalf("record %d: %v", i+1, err)
		}
	}

	if ok, _, _ := c.Check(ctx, key); ok {
		t.Fatal("the counter is not spent after reaching its limit")
	}

	if err := c.Reset(ctx, key); err != nil {
		t.Fatalf("Reset(): %v", err)
	}

	ok, _, err := c.Check(ctx, key)
	if err != nil {
		t.Fatalf("check after reset: %v", err)
	}

	if !ok {
		t.Error("still refused after a reset; success does not clear the count")
	}
}

// Reset has to reach every tier, not just the first. Clearing the tight window
// and leaving the loose one would let a successful login look forgiven for a
// minute and then start refusing again for reasons nobody can see.
func TestResetClearsEveryTier(t *testing.T) {
	t.Parallel()

	c, key := counter(t,
		redis.Tier{Limit: 1, Window: 30 * time.Second},
		redis.Tier{Limit: 5, Window: time.Minute},
	)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	for range 5 {
		if err := c.Record(ctx, key); err != nil {
			t.Fatalf("Record(): %v", err)
		}
	}

	if err := c.Reset(ctx, key); err != nil {
		t.Fatalf("Reset(): %v", err)
	}

	if ok, _, err := c.Check(ctx, key); err != nil || !ok {
		t.Errorf("check after reset: ok=%v err=%v; a tier survived the reset", ok, err)
	}
}

// Every tier is enforced, not just the first. ADR-0010 pairs a tight window
// with a loose one so that a guesser who waits between rounds still runs into
// the loose one.
func TestEveryTierRefusesOnItsOwn(t *testing.T) {
	t.Parallel()

	c, key := counter(t,
		redis.Tier{Limit: 2, Window: 500 * time.Millisecond},
		redis.Tier{Limit: 3, Window: time.Minute},
	)

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	for range 2 {
		if err := c.Record(ctx, key); err != nil {
			t.Fatalf("Record(): %v", err)
		}
	}

	if ok, _, _ := c.Check(ctx, key); ok {
		t.Fatal("the tight tier did not refuse at its limit")
	}

	// The tight tier ages out; the minute-long one keeps both failures.
	time.Sleep(600 * time.Millisecond)

	if ok, _, err := c.Check(ctx, key); err != nil || !ok {
		t.Fatalf("check after the tight tier aged out: ok=%v err=%v", ok, err)
	}

	if err := c.Record(ctx, key); err != nil {
		t.Fatalf("Record(): %v", err)
	}

	// Three failures now sit in the loose tier, which is its whole limit, while
	// the tight tier holds one of two. Only the loose tier can refuse this.
	ok, retryAfter, err := c.Check(ctx, key)
	if err != nil {
		t.Fatalf("check over the loose tier: %v", err)
	}

	if ok {
		t.Error("the loose tier never refused; only the tight tier is being enforced")
	}

	if retryAfter <= time.Second {
		t.Errorf("retryAfter = %s, want the loose tier's remaining time", retryAfter)
	}
}

// Failures age out on their own clocks, so a guesser cannot be locked out
// forever and an honest user who failed once an hour ago is not still paying
// for it.
func TestFailuresAgeOut(t *testing.T) {
	t.Parallel()

	c, key := counter(t, redis.Tier{Limit: 1, Window: 400 * time.Millisecond})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	if err := c.Record(ctx, key); err != nil {
		t.Fatalf("Record(): %v", err)
	}

	if ok, _, _ := c.Check(ctx, key); ok {
		t.Fatal("not refused immediately after reaching the limit")
	}

	time.Sleep(500 * time.Millisecond)

	ok, _, err := c.Check(ctx, key)
	if err != nil {
		t.Fatalf("check after the window: %v", err)
	}

	if !ok {
		t.Error("the failure never aged out; the lockout is permanent")
	}
}

// Failures landing in the same millisecond must count separately. They share a
// score, so without a distinct member the second ZADD updates the first instead
// of adding to it and the pair counts once — quietly widening the limit under
// exactly the concurrent load it exists to stop.
//
// Recording sequentially does not test this: the round trips are far enough
// apart that they usually land in different milliseconds, and the test passes
// with the sequence number removed. Only concurrency makes the collision happen.
func TestFailuresInTheSameMillisecondCountSeparately(t *testing.T) {
	t.Parallel()

	const failures = 50

	c, key := counter(t, redis.Tier{Limit: failures, Window: time.Minute})

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	var wg sync.WaitGroup

	for range failures {
		wg.Add(1)

		go func() {
			defer wg.Done()

			if err := c.Record(ctx, key); err != nil {
				t.Errorf("Record(): %v", err)
			}
		}()
	}

	wg.Wait()

	// Exactly as many failures as the limit, so anything that collapsed leaves
	// the counter one short and this check comes back with room to spare.
	ok, _, err := c.Check(ctx, key)
	if err != nil {
		t.Fatalf("Check(): %v", err)
	}

	if ok {
		t.Errorf("%d concurrent failures did not reach a limit of %d; some shared a member", failures, failures)
	}
}

// Failures are counted per key. One account being guessed at must not spend
// everybody else's allowance.
func TestKeysFailSeparately(t *testing.T) {
	t.Parallel()

	client, err := redis.New(testRedisURL(t), 5)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	c := redis.NewFailureCounter(client, redis.Tier{Limit: 1, Window: time.Minute})
	prefix := keyPrefix(t)

	if err := c.Record(ctx(t), prefix+"first"); err != nil {
		t.Fatalf("Record(): %v", err)
	}

	if ok, _, _ := c.Check(ctx(t), prefix+"first"); ok {
		t.Error("the first key was not refused after spending its limit")
	}

	if ok, _, err := c.Check(ctx(t), prefix+"second"); err != nil || !ok {
		t.Errorf("the second key was refused: ok=%v err=%v", ok, err)
	}
}

// A dead Redis is an error, never a verdict. The caller fails the
// authentication path closed and everything else open, and it can only do that
// if a failure arrives as one.
func TestADeadRedisIsNeverAVerdict(t *testing.T) {
	t.Parallel()

	client, err := redis.New("redis://127.0.0.1:1", 5)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	c := redis.NewFailureCounter(client, redis.Tier{Limit: 5, Window: time.Minute})

	ok, _, err := c.Check(ctx(t), "anything")
	if err == nil {
		t.Error("Check() against a dead Redis returned no error")
	}

	if ok {
		t.Error("Check() reported room while it could not count anything")
	}

	if err := c.Record(ctx(t), "anything"); err == nil {
		t.Error("Record() against a dead Redis returned no error")
	}

	if err := c.Reset(ctx(t), "anything"); err == nil {
		t.Error("Reset() against a dead Redis returned no error")
	}
}

// A counter with nothing configured refuses nothing, which is what an empty
// tier list means. Saying so here is cheaper than a nil dereference on the
// first login of a path somebody forgot to configure.
func TestACounterWithNoTiersRefusesNothing(t *testing.T) {
	t.Parallel()

	c, key := counter(t)

	if err := c.Record(ctx(t), key); err != nil {
		t.Fatalf("Record(): %v", err)
	}

	if err := c.Reset(ctx(t), key); err != nil {
		t.Fatalf("Reset(): %v", err)
	}

	if ok, _, err := c.Check(ctx(t), key); err != nil || !ok {
		t.Errorf("Check() = %v, %v; a counter with no limits refused something", ok, err)
	}
}

func ctx(t *testing.T) context.Context {
	t.Helper()

	c, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)

	return c
}
