package service_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	identitysvc "github.com/ragbuaj/project-management/backend/internal/modules/identity/service"
)

// counterStub stands in for one bucket in Redis.
type counterStub struct {
	refuseFor time.Duration // non-zero means this bucket is spent
	failWith  error

	checked  []string
	recorded []string
	reset    []string
}

func (c *counterStub) Check(_ context.Context, key string) (bool, time.Duration, error) {
	if c.failWith != nil {
		return false, 0, c.failWith
	}

	c.checked = append(c.checked, key)

	if c.refuseFor > 0 {
		return false, c.refuseFor, nil
	}

	return true, 0, nil
}

func (c *counterStub) Record(_ context.Context, key string) error {
	if c.failWith != nil {
		return c.failWith
	}

	c.recorded = append(c.recorded, key)

	return nil
}

func (c *counterStub) Reset(_ context.Context, key string) error {
	if c.failWith != nil {
		return c.failWith
	}

	c.reset = append(c.reset, key)

	return nil
}

type guardParts struct {
	guard                     *identitysvc.LoginGuard
	account, address, network *counterStub
}

func newGuard(t *testing.T) guardParts {
	t.Helper()

	account, address, network := &counterStub{}, &counterStub{}, &counterStub{}

	return guardParts{
		guard:   identitysvc.NewLoginGuard(account, address, network, slog.New(slog.DiscardHandler)),
		account: account,
		address: address,
		network: network,
	}
}

func anAttempt() identitysvc.LoginAttempt {
	return identitysvc.LoginAttempt{
		Email:   "budi@example.com",
		Address: "203.0.113.1",
		Network: "203.0.113.0/24",
	}
}

func TestAnAttemptUnderEveryLimitProceeds(t *testing.T) {
	t.Parallel()

	parts := newGuard(t)

	retryAfter, err := parts.guard.Check(t.Context(), anAttempt())
	if err != nil {
		t.Fatalf("Check() = %v, want no error", err)
	}

	if retryAfter != 0 {
		t.Errorf("retryAfter = %s, want zero when the attempt may proceed", retryAfter)
	}

	// Checking must not count. A correct password is the common case, and
	// counting it would throttle the people who never got anything wrong.
	if len(parts.account.recorded) > 0 || len(parts.address.recorded) > 0 {
		t.Error("Check() recorded something; it must only read")
	}
}

// Each bucket must be able to refuse on its own. A guard that only ever
// consults the account bucket looks identical from the outside until somebody
// spreads their guessing across accounts.
func TestEachBucketCanRefuseOnItsOwn(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		spend func(guardParts)
	}{
		{"account", func(p guardParts) { p.account.refuseFor = time.Minute }},
		{"address", func(p guardParts) { p.address.refuseFor = time.Minute }},
		{"network", func(p guardParts) { p.network.refuseFor = time.Minute }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			parts := newGuard(t)
			tc.spend(parts)

			retryAfter, err := parts.guard.Check(t.Context(), anAttempt())
			if !errors.Is(err, identitysvc.ErrTooManyAttempts) {
				t.Fatalf("Check() = %v, want ErrTooManyAttempts when the %s bucket is spent", err, tc.name)
			}

			if retryAfter != time.Minute {
				t.Errorf("retryAfter = %s, want the refusing bucket's wait", retryAfter)
			}
		})
	}
}

// The caller is told the longest wait, not the first one found. A shorter
// answer invites a retry that is refused again, which reads as the limit lying.
func TestTheLongestWaitWins(t *testing.T) {
	t.Parallel()

	parts := newGuard(t)
	parts.account.refuseFor = time.Minute
	parts.network.refuseFor = time.Hour

	retryAfter, err := parts.guard.Check(t.Context(), anAttempt())
	if !errors.Is(err, identitysvc.ErrTooManyAttempts) {
		t.Fatalf("Check() = %v, want ErrTooManyAttempts", err)
	}

	if retryAfter != time.Hour {
		t.Errorf("retryAfter = %s, want the longest of the refusing buckets (1h)", retryAfter)
	}
}

// docs/nfr.md asks this path specifically to fail closed. Everywhere else a
// Redis outage degrades a feature; here it would remove the only thing between
// a leaked address list and every account.
func TestACounterThatWillNotAnswerRefusesTheAttempt(t *testing.T) {
	t.Parallel()

	parts := newGuard(t)
	parts.address.failWith = errors.New("redis is down")

	retryAfter, err := parts.guard.Check(t.Context(), anAttempt())
	if !errors.Is(err, identitysvc.ErrTooManyAttempts) {
		t.Fatalf("Check() = %v, want ErrTooManyAttempts; the guard failed open", err)
	}

	if retryAfter <= 0 {
		t.Errorf("retryAfter = %s, want a wait the client can act on", retryAfter)
	}
}

func TestAFailedAttemptIsCountedEverywhere(t *testing.T) {
	t.Parallel()

	parts := newGuard(t)

	parts.guard.RecordFailure(t.Context(), anAttempt())

	for name, stub := range map[string]*counterStub{
		"account": parts.account,
		"address": parts.address,
		"network": parts.network,
	} {
		if len(stub.recorded) != 1 {
			t.Errorf("the %s bucket recorded %d failures, want 1", name, len(stub.recorded))
		}
	}
}

// ADR-0010 clears the counter on success, so four typos followed by the right
// password does not leave somebody one attempt from a lockout.
func TestSuccessClearsTheAccountBucket(t *testing.T) {
	t.Parallel()

	parts := newGuard(t)

	parts.guard.Succeeded(t.Context(), anAttempt())

	if len(parts.account.reset) != 1 {
		t.Fatalf("the account bucket was reset %d times, want 1", len(parts.account.reset))
	}
}

// The address and network buckets count failures from everyone behind them, so
// clearing them on one person's success hands an attacker a reset button: sign
// in to an account they already own, and the bucket guarding everybody else is
// empty again.
func TestSuccessDoesNotClearTheSharedBuckets(t *testing.T) {
	t.Parallel()

	parts := newGuard(t)

	parts.guard.Succeeded(t.Context(), anAttempt())

	if len(parts.address.reset) > 0 {
		t.Error("the address bucket was cleared by one account's success")
	}

	if len(parts.network.reset) > 0 {
		t.Error("the network bucket was cleared by one account's success")
	}
}

// The account lookup compares lower(email), so a bucket that told the two apart
// would be defeated by pressing shift.
func TestTheAccountBucketIgnoresCase(t *testing.T) {
	t.Parallel()

	parts := newGuard(t)

	lower := anAttempt()

	upper := anAttempt()
	upper.Email = "Budi@Example.COM"

	if _, err := parts.guard.Check(t.Context(), lower); err != nil {
		t.Fatalf("Check(lower): %v", err)
	}

	if _, err := parts.guard.Check(t.Context(), upper); err != nil {
		t.Fatalf("Check(upper): %v", err)
	}

	if parts.account.checked[0] != parts.account.checked[1] {
		t.Errorf("%q and %q key differently: %q vs %q",
			lower.Email, upper.Email, parts.account.checked[0], parts.account.checked[1])
	}
}

// The submitted address must not appear in the key. Nothing ever reads it back,
// and a datastore that holds a list of who has been trying to sign in is
// holding something rules/45-privacy says it should not.
func TestTheSubmittedAddressIsNotInTheKey(t *testing.T) {
	t.Parallel()

	parts := newGuard(t)

	if _, err := parts.guard.Check(t.Context(), anAttempt()); err != nil {
		t.Fatalf("Check(): %v", err)
	}

	if got := parts.account.checked[0]; strings.Contains(got, "budi") || strings.Contains(got, "example.com") {
		t.Errorf("key %q carries the address it was built from", got)
	}
}

// A counter that will not write must not change what the caller is told. An
// endpoint whose answer depends on something other than the credentials is an
// endpoint that says which addresses have accounts.
func TestAWriteThatFailsDoesNotChangeTheOutcome(t *testing.T) {
	t.Parallel()

	parts := newGuard(t)
	parts.account.failWith = errors.New("redis is down")
	parts.address.failWith = errors.New("redis is down")
	parts.network.failWith = errors.New("redis is down")

	// Neither of these returns anything, and neither may panic or block: the
	// login has already been decided by the time they run.
	parts.guard.RecordFailure(t.Context(), anAttempt())
	parts.guard.Succeeded(t.Context(), anAttempt())
}

// The numbers are ADR-0010's, and the shape it insists on is two windows per
// bucket — a tight one to slow a guesser down, a loose one to catch one who
// waits between rounds.
func TestEveryBucketHasATightWindowAndALooseOne(t *testing.T) {
	t.Parallel()

	limits := identitysvc.DefaultLoginLimits()

	for name, buckets := range map[string][]identitysvc.Bucket{
		"account": limits.Account,
		"address": limits.Address,
		"network": limits.Network,
	} {
		if len(buckets) != 2 {
			t.Errorf("the %s bucket has %d windows, want a tight one and a loose one", name, len(buckets))

			continue
		}

		tight, loose := buckets[0], buckets[1]

		if tight.Window >= loose.Window {
			t.Errorf("the %s windows are %s and %s; the second is not looser", name, tight.Window, loose.Window)
		}

		if tight.Limit >= loose.Limit {
			t.Errorf("the %s limits are %d and %d; the looser window does not allow more",
				name, tight.Limit, loose.Limit)
		}
	}

	// The shared buckets have to be looser than the account one, or an office
	// behind one address locks itself out before any single account does.
	if limits.Address[0].Limit <= limits.Account[0].Limit {
		t.Error("the address bucket is no looser than the account bucket")
	}

	if limits.Network[0].Limit <= limits.Address[0].Limit {
		t.Error("the network bucket is no looser than the address bucket")
	}
}
