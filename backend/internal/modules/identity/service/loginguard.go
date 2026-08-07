package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"time"
)

// FailureCounter counts attempts that failed, and is cleared when one finally
// succeeds. It is declared here, where it is used, rather than beside its
// implementation (ADR-0008).
type FailureCounter interface {
	Check(ctx context.Context, key string) (ok bool, retryAfter time.Duration, err error)
	Record(ctx context.Context, key string) error
	Reset(ctx context.Context, key string) error
}

// ErrTooManyAttempts refuses an attempt before the password is even looked at.
//
// It covers being over a limit and being unable to tell, because the caller is
// told the same thing either way: wait. Which one it was belongs in the log,
// where somebody can act on it.
var ErrTooManyAttempts = errors.New("too many login attempts")

// Bucket is one limit over one window.
type Bucket struct {
	Limit  int
	Window time.Duration
}

// LoginLimits is ADR-0010's table, written where the code that enforces it can
// be read next to it.
//
// The numbers are starting points the ADR says may be tuned; the shape is not.
// Every bucket has two windows — a tight one to slow a guesser down and a loose
// one to catch a guesser who waits between rounds — because a single window can
// only be right for one of those.
type LoginLimits struct {
	Account []Bucket
	Address []Bucket
	Network []Bucket
}

// DefaultLoginLimits returns the numbers from ADR-0010.
//
// The account and address rows are the ADR's own. **The network row is not:
// ADR-0010 has no line for it**, so these two numbers were chosen here and are
// the ones to argue with first. They are ten times the per-address allowance
// over the same window, which is loose enough that an office or a campus behind
// one /24 never reaches it, and tight enough that a spread designed to keep
// every single address under thirty is still caught at three hundred.
func DefaultLoginLimits() LoginLimits {
	return LoginLimits{
		Account: []Bucket{
			{Limit: 5, Window: 15 * time.Minute},
			{Limit: 20, Window: time.Hour},
		},
		Address: []Bucket{
			{Limit: 30, Window: 10 * time.Minute},
			{Limit: 200, Window: 24 * time.Hour},
		},
		Network: []Bucket{
			{Limit: 300, Window: 10 * time.Minute},
			{Limit: 2000, Window: 24 * time.Hour},
		},
	}
}

// retryAfterUnavailable is what a caller is told to wait when the counters
// cannot be read at all. The limiter could not say, so this is a guess — short
// enough that a client retries rather than gives up, long enough not to become
// a tight loop against a Redis that is already struggling.
const retryAfterUnavailable = 30 * time.Second

// LoginAttempt is who is trying, reduced to the three things the buckets count.
//
// The address strings arrive already aggregated — /64 and /48, /24 for IPv4 —
// because that is an addressing decision and it lives in internal/httpx with
// the code that works out which address to believe in the first place. This
// package decides policy, not what an address means.
type LoginAttempt struct {
	Email   string
	Address string
	Network string
}

// LoginGuard decides whether a login attempt may be tried at all.
//
// It is three separate counters rather than one because each answers a
// different question, and no single one of them can be set correctly. The
// account bucket is the only one that can be tight, and it is also the only one
// an attacker can aim at somebody else — so it must throttle rather than lock,
// which is what a short window plus a longer one does. The address and network
// buckets have to stay loose, because a whole office or campus can sit behind
// either, and they exist to catch the spread that a tight account bucket alone
// would miss.
type LoginGuard struct {
	account FailureCounter
	address FailureCounter
	network FailureCounter
	log     *slog.Logger
}

func NewLoginGuard(account, address, network FailureCounter, log *slog.Logger) *LoginGuard {
	return &LoginGuard{account: account, address: address, network: network, log: log}
}

// Check reports whether the attempt may proceed to the password check.
//
// It returns ErrTooManyAttempts and a wait when it may not. The wait is the
// longest of every bucket that refused, not the first one found: a shorter
// answer invites a retry that is refused again, which from the outside reads as
// the limit lying about itself.
//
// It fails closed. When the counters cannot be read, the attempt is refused —
// docs/nfr.md asks for that on this path specifically, because an unguarded
// login endpoint is worse than one that is briefly unavailable. Everywhere else
// in the application a Redis outage degrades a feature; here it would remove
// the only thing standing between a leaked address list and every account.
func (g *LoginGuard) Check(ctx context.Context, attempt LoginAttempt) (retryAfter time.Duration, err error) {
	var longest time.Duration

	for _, bucket := range g.bucketsFor(attempt) {
		ok, wait, err := bucket.counter.Check(ctx, bucket.key)
		if err != nil {
			// The key is not logged: it is an address or a hashed account, and
			// this line is written on every attempt while Redis is down.
			g.log.ErrorContext(ctx, "login rate limit unavailable; failing closed",
				slog.String("bucket", bucket.name),
				slog.String("error", err.Error()))

			return retryAfterUnavailable, ErrTooManyAttempts
		}

		if !ok {
			longest = max(longest, wait)
		}
	}

	if longest > 0 {
		return longest, ErrTooManyAttempts
	}

	return 0, nil
}

// RecordFailure counts one failed attempt against all three buckets.
//
// A counter that will not write is logged and the login continues to its 401.
// The alternative is answering differently when Redis is unwell, and a login
// endpoint whose answer depends on something other than the credentials is an
// endpoint that tells you which addresses have accounts.
func (g *LoginGuard) RecordFailure(ctx context.Context, attempt LoginAttempt) {
	for _, bucket := range g.bucketsFor(attempt) {
		if err := bucket.counter.Record(ctx, bucket.key); err != nil {
			g.log.ErrorContext(ctx, "a failed login was not counted",
				slog.String("bucket", bucket.name),
				slog.String("error", err.Error()))
		}
	}
}

// Succeeded clears the account's failures, and only the account's.
//
// ADR-0010 clears the counter on success so that somebody who mistypes four
// times and then gets it right is not left one attempt from being locked out.
//
// The address and network buckets are deliberately left alone. They count
// failures from everyone behind that address, so clearing them on one person's
// success would hand an attacker a reset button: sign in to an account they
// already own, and the bucket guarding everybody else's is empty again.
//
// A failure here is logged and the login proceeds. Refusing a correct password
// because a counter would not clear takes something away from the one person
// who did nothing wrong.
func (g *LoginGuard) Succeeded(ctx context.Context, attempt LoginAttempt) {
	if err := g.account.Reset(ctx, accountKey(attempt.Email)); err != nil {
		g.log.ErrorContext(ctx, "failed logins were not cleared after a successful one",
			slog.String("error", err.Error()))
	}
}

// bucket pairs a counter with the key it counts against and a name for the log.
type bucket struct {
	name    string
	counter FailureCounter
	key     string
}

func (g *LoginGuard) bucketsFor(attempt LoginAttempt) []bucket {
	return []bucket{
		{name: "account", counter: g.account, key: accountKey(attempt.Email)},
		{name: "address", counter: g.address, key: "ip:" + attempt.Address},
		{name: "network", counter: g.network, key: "net:" + attempt.Network},
	}
}

// accountKey turns a submitted address into the key its bucket counts against.
//
// It is lower-cased first because the account lookup is (users.sql compares
// lower(email)), and a bucket that told Alice@example.com apart from
// alice@example.com would be defeated by pressing shift.
//
// It is hashed for one reason only, and it is not secrecy: an address is
// guessable, so this stops nobody who is trying. It keeps a list of who has
// been attempting to sign in out of a datastore that has no business holding
// one — rules/45-privacy asks for exactly that, and nothing here ever needs to
// read the address back. It also bounds the key length, which a 1 KiB address
// otherwise would not.
func accountKey(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))

	return "account:" + hex.EncodeToString(sum[:16])
}
