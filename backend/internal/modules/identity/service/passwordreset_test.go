package service_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ragbuaj/project-management/backend/internal/mail"
	identitydom "github.com/ragbuaj/project-management/backend/internal/modules/identity/domain"
	identityrepo "github.com/ragbuaj/project-management/backend/internal/modules/identity/repository"
	identitysvc "github.com/ragbuaj/project-management/backend/internal/modules/identity/service"
)

var requestedAt = time.Date(2026, time.August, 8, 9, 0, 0, 0, time.UTC)

// resetStore records what it was asked to do, in order, so a test can assert
// that the previous link was closed before a new one was written rather than
// merely that both happened.
type resetStore struct {
	calls []string

	account   *identityrepo.GetUserByEmailRow
	lookupErr error
	expireErr error
	createErr error

	created identityrepo.CreatePasswordResetParams
}

func (s *resetStore) GetUserByEmail(_ context.Context, _ string) (identityrepo.GetUserByEmailRow, error) {
	s.calls = append(s.calls, "lookup")

	if s.lookupErr != nil {
		return identityrepo.GetUserByEmailRow{}, s.lookupErr
	}

	if s.account == nil {
		return identityrepo.GetUserByEmailRow{}, pgx.ErrNoRows
	}

	return *s.account, nil
}

func (s *resetStore) ExpireOpenPasswordResetsForUser(_ context.Context, _ string) (int64, error) {
	s.calls = append(s.calls, "expire")

	return 1, s.expireErr
}

func (s *resetStore) CreatePasswordReset(_ context.Context, arg identityrepo.CreatePasswordResetParams) (identityrepo.CreatePasswordResetRow, error) {
	s.calls = append(s.calls, "create")

	if s.createErr != nil {
		return identityrepo.CreatePasswordResetRow{}, s.createErr
	}

	s.created = arg

	return identityrepo.CreatePasswordResetRow{
		ID:        "55555555-5555-5555-5555-555555555555",
		UserID:    arg.UserID,
		ExpiresAt: arg.ExpiresAt,
	}, nil
}

// The invitation half. One transaction boundary means one store interface; no
// reset test reaches these.
func (s *resetStore) CreateInvitation(context.Context, identityrepo.CreateInvitationParams) (identityrepo.CreateInvitationRow, error) {
	return identityrepo.CreateInvitationRow{}, nil
}

func (s *resetStore) ExpireOpenInvitationsForEmail(context.Context, string) (int64, error) {
	return 0, nil
}

func (s *resetStore) GetInvitationByTokenHash(context.Context, []byte) (identityrepo.GetInvitationByTokenHashRow, error) {
	return identityrepo.GetInvitationByTokenHashRow{}, pgx.ErrNoRows
}

func (s *resetStore) AcceptInvitation(context.Context, string) (int64, error) { return 0, nil }

func (s *resetStore) CreateUser(context.Context, identityrepo.CreateUserParams) (identityrepo.CreateUserRow, error) {
	return identityrepo.CreateUserRow{}, nil
}

// fakeLimiter is the request bucket. allow=false is a spent bucket; err is Redis
// refusing to answer, which must fail closed.
type fakeLimiter struct {
	allow bool
	wait  time.Duration
	err   error

	keys []string
}

func (l *fakeLimiter) Allow(_ context.Context, key string) (bool, time.Duration, error) {
	l.keys = append(l.keys, key)

	if l.err != nil {
		return false, 0, l.err
	}

	return l.allow, l.wait, nil
}

func resets(t *testing.T, store *resetStore, limiter *fakeLimiter, commitErr error) (*identitysvc.PasswordResets, *mail.Capture) {
	t.Helper()

	capture := mail.NewCapture(sentFrom)

	base, err := url.Parse("https://pm.example.test")
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}

	inTx := func(_ context.Context, fn func(identitysvc.TxStore) error) error {
		if err := fn(store); err != nil {
			return err
		}

		return commitErr
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	service := identitysvc.NewPasswordResets(inTx, capture, limiter, base, log,
		func() time.Time { return requestedAt })

	return service, capture
}

func account() *identityrepo.GetUserByEmailRow {
	return &identityrepo.GetUserByEmailRow{
		ID:    "66666666-6666-6666-6666-666666666666",
		Email: "Budi@example.test",
		Name:  "Budi",
		Role:  "contributor",
	}
}

func open() *fakeLimiter { return &fakeLimiter{allow: true} }

func TestAResetIsRecordedAndItsLinkSent(t *testing.T) {
	t.Parallel()

	store := &resetStore{account: account()}

	service, capture := resets(t, store, open(), nil)

	if _, err := service.Request(t.Context(), "budi@example.test"); err != nil {
		t.Fatalf("Request: %v", err)
	}

	if want := requestedAt.Add(identitydom.PasswordResetWindow); !store.created.ExpiresAt.Time.Equal(want) {
		t.Errorf("deadline %v, want %v", store.created.ExpiresAt.Time, want)
	}

	delivery, ok := capture.Last()
	if !ok {
		t.Fatal("no message was sent")
	}

	// The address the account holds, not the one that was typed. They differ
	// only in case, and the account's is the one somebody chose for themselves.
	if delivery.Message.To != "Budi@example.test" {
		t.Errorf("message went to %q, want the address as the account holds it", delivery.Message.To)
	}

	if !strings.Contains(delivery.Message.Text, "https://pm.example.test/reset-password/") {
		t.Errorf("the message carries no reset link:\n%s", delivery.Message.Text)
	}
}

// The property the stored digest depends on: what is written to the database
// must be the hash of the token that went out, and the token must exist nowhere
// else. If these ever drift apart no reset can be confirmed at all.
func TestTheResetLinkIsTheOnlyCopyOfTheToken(t *testing.T) {
	t.Parallel()

	store := &resetStore{account: account()}

	service, capture := resets(t, store, open(), nil)

	if _, err := service.Request(t.Context(), "budi@example.test"); err != nil {
		t.Fatalf("Request: %v", err)
	}

	delivery, _ := capture.Last()

	_, after, found := strings.Cut(delivery.Message.Text, "/reset-password/")
	if !found {
		t.Fatalf("no link in:\n%s", delivery.Message.Text)
	}

	token := strings.TrimSpace(strings.SplitN(after, "\n", 2)[0])

	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("the token in the link is not a token: %v", err)
	}

	sum := sha256.Sum256(raw)

	if string(store.created.TokenHash) != string(sum[:]) {
		t.Error("the stored digest is not the hash of the token that was sent")
	}
}

// The whole point of the endpoint's shape. An address with no account is
// answered exactly like one with an account, and nothing is sent.
func TestAnAddressWithNoAccountIsAnsweredTheSameWayAndSendsNothing(t *testing.T) {
	t.Parallel()

	known := &resetStore{account: account()}
	knownService, knownMail := resets(t, known, open(), nil)

	unknown := &resetStore{}
	unknownService, unknownMail := resets(t, unknown, open(), nil)

	wait, err := knownService.Request(t.Context(), "budi@example.test")
	if err != nil {
		t.Fatalf("Request for a known address: %v", err)
	}

	otherWait, otherErr := unknownService.Request(t.Context(), "nobody@example.test")
	if otherErr != nil {
		t.Fatalf("Request for an unknown address: %v", otherErr)
	}

	if wait != otherWait {
		t.Errorf("the two answers differ: %v against %v", wait, otherWait)
	}

	if len(unknownMail.Sent()) != 0 {
		t.Error("a message was sent for an address with no account")
	}

	if len(knownMail.Sent()) != 1 {
		t.Errorf("%d messages sent for the known address, want 1", len(knownMail.Sent()))
	}

	// Nothing was written either. A row for an account that does not exist could
	// not be inserted anyway -- the foreign key would refuse it -- so reaching
	// the statement at all would be an error the caller would then have to be
	// told about, and that is the enumeration back again.
	if slices.Contains(unknown.calls, "create") {
		t.Errorf("calls were %v; nothing may be written for an address with no account", unknown.calls)
	}
}

// Clicking "forgot password" four times must not leave four ways in. The order
// is asserted, not just the fact that both happened.
func TestAskingAgainClosesTheEarlierLinkBeforeWritingTheNewOne(t *testing.T) {
	t.Parallel()

	store := &resetStore{account: account()}

	service, _ := resets(t, store, open(), nil)

	if _, err := service.Request(t.Context(), "budi@example.test"); err != nil {
		t.Fatalf("Request: %v", err)
	}

	if want := []string{"lookup", "expire", "create"}; !slices.Equal(store.calls, want) {
		t.Errorf("the store was asked to do %v, want %v", store.calls, want)
	}
}

// The deliberate opposite of Invitations.Create. A 500 here would arrive only
// when the address has an account, which hands back the enumeration the
// constant answer exists to deny.
func TestADeliveryFailureIsSwallowedRatherThanReported(t *testing.T) {
	t.Parallel()

	store := &resetStore{account: account()}

	service, capture := resets(t, store, open(), nil)
	capture.Fail(errors.New("the mail server is not answering"))

	wait, err := service.Request(t.Context(), "budi@example.test")
	if err != nil {
		t.Errorf("Request returned %v; a delivery failure must not be reportable, or a 500 tells the caller the address has an account", err)
	}

	if wait != 0 {
		t.Errorf("retryAfter = %v, want 0", wait)
	}

	// The row is left behind on purpose: it carries no address anybody can reach
	// it by, and it expires within the hour.
	if !slices.Contains(store.calls, "create") {
		t.Error("the reset was not recorded")
	}
}

// The message goes after the commit, never inside the transaction: an SMTP round
// trip inside one is a lock on password_resets held for as long as the mail
// server feels like taking.
func TestNoResetLinkIsSentWhenTheTransactionCannotCommit(t *testing.T) {
	t.Parallel()

	store := &resetStore{account: account()}

	service, capture := resets(t, store, open(), errors.New("commit: connection reset"))

	if _, err := service.Request(t.Context(), "budi@example.test"); err == nil {
		t.Error("Request succeeded although the transaction did not commit")
	}

	if len(capture.Sent()) != 0 {
		t.Error("a link was sent for a reset that was never written")
	}
}

func TestAnAddressThatIsNotOneIsRefusedBeforeTheBucketIsSpent(t *testing.T) {
	t.Parallel()

	for name, address := range map[string]string{
		"empty":           "",
		"not an address":  "budi",
		"a display name":  "Budi <budi@example.test>",
		"longer than 254": strings.Repeat("a", 250) + "@example.test",
		"only whitespace": "   ",
		"two addresses":   "budi@example.test, siti@example.test",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := &resetStore{account: account()}
			limiter := open()

			service, capture := resets(t, store, limiter, nil)

			if _, err := service.Request(t.Context(), address); !errors.Is(err, identitysvc.ErrInvalidEmail) {
				t.Errorf("Request(%q) = %v, want ErrInvalidEmail", address, err)
			}

			if len(limiter.keys) != 0 {
				t.Error("a malformed address spent somebody's allowance")
			}

			if len(capture.Sent()) != 0 {
				t.Error("a message was sent for an address that is not one")
			}
		})
	}
}

// ADR-0010 bounds this at three an hour per account. Being over it must stop the
// request before anything is written and before anything is sent.
func TestASpentBucketRefusesBeforeAnythingIsWrittenOrSent(t *testing.T) {
	t.Parallel()

	store := &resetStore{account: account()}
	limiter := &fakeLimiter{allow: false, wait: 12 * time.Minute}

	service, capture := resets(t, store, limiter, nil)

	wait, err := service.Request(t.Context(), "budi@example.test")
	if !errors.Is(err, identitysvc.ErrTooManyAttempts) {
		t.Fatalf("Request = %v, want ErrTooManyAttempts", err)
	}

	if wait != 12*time.Minute {
		t.Errorf("retryAfter = %v, want the limiter's own answer of 12m", wait)
	}

	if len(store.calls) != 0 {
		t.Errorf("the store was asked to do %v after the bucket refused", store.calls)
	}

	if len(capture.Sent()) != 0 {
		t.Error("a message was sent after the bucket refused")
	}
}

// An unauthenticated endpoint that causes mail to land in somebody else's inbox
// fails closed, the same call ADR-0010 makes for login: a limiter that is off is
// indistinguishable from one that was never installed.
func TestALimiterThatCannotAnswerRefusesTheRequest(t *testing.T) {
	t.Parallel()

	store := &resetStore{account: account()}
	limiter := &fakeLimiter{allow: true, err: errors.New("redis: connection refused")}

	service, capture := resets(t, store, limiter, nil)

	wait, err := service.Request(t.Context(), "budi@example.test")
	if !errors.Is(err, identitysvc.ErrTooManyAttempts) {
		t.Fatalf("Request = %v, want ErrTooManyAttempts", err)
	}

	if wait <= 0 {
		t.Errorf("retryAfter = %v, want a wait the caller can act on", wait)
	}

	if len(store.calls) != 0 || len(capture.Sent()) != 0 {
		t.Error("the request went ahead although the limit could not be checked")
	}
}

// The bucket is keyed by the address, hashed, and case cannot be used to get a
// second allowance -- users_email_key indexes lower(email), so the two spellings
// are one account.
func TestTheBucketKeyIsTheHashedAddressAndIgnoresCase(t *testing.T) {
	t.Parallel()

	limiter := open()

	service, _ := resets(t, &resetStore{account: account()}, limiter, nil)

	for _, spelling := range []string{"budi@example.test", "BUDI@Example.test"} {
		if _, err := service.Request(t.Context(), spelling); err != nil {
			t.Fatalf("Request(%q): %v", spelling, err)
		}
	}

	if len(limiter.keys) != 2 {
		t.Fatalf("the bucket was consulted %d times, want 2", len(limiter.keys))
	}

	if limiter.keys[0] != limiter.keys[1] {
		t.Errorf("two spellings of one address keyed %q and %q; pressing shift buys a second allowance",
			limiter.keys[0], limiter.keys[1])
	}

	if strings.Contains(limiter.keys[0], "budi") || strings.Contains(strings.ToLower(limiter.keys[0]), "example.test") {
		t.Errorf("the key %q carries the address; rules/45-privacy keeps a list of who is asking out of Redis", limiter.keys[0])
	}
}
