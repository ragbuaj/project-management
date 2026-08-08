package service_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	netmail "net/mail"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ragbuaj/project-management/backend/internal/mail"
	identitydom "github.com/ragbuaj/project-management/backend/internal/modules/identity/domain"
	identityrepo "github.com/ragbuaj/project-management/backend/internal/modules/identity/repository"
	identitysvc "github.com/ragbuaj/project-management/backend/internal/modules/identity/service"
)

var (
	invitedAt = time.Date(2026, time.August, 8, 9, 0, 0, 0, time.UTC)
	inviter   = "11111111-1111-1111-1111-111111111111"
	sentFrom  = netmail.Address{Name: "Project Management", Address: "no-reply@pm.example.test"}
)

// inviteStore records what it was asked to do, in order, so a test can assert
// that the previous link was closed before a new one was written rather than
// merely that both happened.
type inviteStore struct {
	calls []string

	existingAccount bool
	createErr       error
	expireErr       error
	lookupErr       error

	created identityrepo.CreateInvitationParams

	// The redemption half. invitation is nil when no row carries the token.
	invitation    *identityrepo.GetInvitationByTokenHashRow
	invitationErr error
	// lostRace makes the stamp update no rows, which is what a redemption that
	// arrived second sees.
	lostRace      bool
	stampErr      error
	createUserErr error

	lookedUp    []byte
	createdUser identityrepo.CreateUserParams
}

func (s *inviteStore) GetUserByEmail(_ context.Context, _ string) (identityrepo.GetUserByEmailRow, error) {
	s.calls = append(s.calls, "lookup")

	if s.lookupErr != nil {
		return identityrepo.GetUserByEmailRow{}, s.lookupErr
	}

	if s.existingAccount {
		return identityrepo.GetUserByEmailRow{ID: "22222222-2222-2222-2222-222222222222"}, nil
	}

	return identityrepo.GetUserByEmailRow{}, pgx.ErrNoRows
}

func (s *inviteStore) ExpireOpenInvitationsForEmail(_ context.Context, _ string) (int64, error) {
	s.calls = append(s.calls, "expire")

	return 1, s.expireErr
}

func (s *inviteStore) CreateInvitation(_ context.Context, arg identityrepo.CreateInvitationParams) (identityrepo.CreateInvitationRow, error) {
	s.calls = append(s.calls, "create")

	if s.createErr != nil {
		return identityrepo.CreateInvitationRow{}, s.createErr
	}

	s.created = arg

	return identityrepo.CreateInvitationRow{
		ID:        "33333333-3333-3333-3333-333333333333",
		Email:     arg.Email,
		Role:      arg.Role,
		InvitedBy: arg.InvitedBy,
		CreatedAt: pgtype.Timestamptz{Time: invitedAt, Valid: true},
		ExpiresAt: arg.ExpiresAt,
	}, nil
}

func (s *inviteStore) GetInvitationByTokenHash(_ context.Context, tokenHash []byte) (identityrepo.GetInvitationByTokenHashRow, error) {
	s.calls = append(s.calls, "read")
	s.lookedUp = tokenHash

	if s.invitationErr != nil {
		return identityrepo.GetInvitationByTokenHashRow{}, s.invitationErr
	}

	if s.invitation == nil {
		return identityrepo.GetInvitationByTokenHashRow{}, pgx.ErrNoRows
	}

	return *s.invitation, nil
}

func (s *inviteStore) AcceptInvitation(_ context.Context, _ string) (int64, error) {
	s.calls = append(s.calls, "stamp")

	if s.stampErr != nil {
		return 0, s.stampErr
	}

	if s.lostRace {
		return 0, nil
	}

	return 1, nil
}

func (s *inviteStore) CreateUser(_ context.Context, arg identityrepo.CreateUserParams) (identityrepo.CreateUserRow, error) {
	s.calls = append(s.calls, "account")

	if s.createUserErr != nil {
		return identityrepo.CreateUserRow{}, s.createUserErr
	}

	s.createdUser = arg

	return identityrepo.CreateUserRow{
		ID:        "44444444-4444-4444-4444-444444444444",
		Email:     arg.Email,
		Name:      arg.Name,
		Timezone:  "Asia/Jakarta",
		Role:      arg.Role,
		CreatedAt: pgtype.Timestamptz{Time: invitedAt, Valid: true},
	}, nil
}

func invitations(t *testing.T, store *inviteStore, commitErr error) (*identitysvc.Invitations, *mail.Capture) {
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

	return identitysvc.NewInvitations(inTx, capture, base, log, func() time.Time { return invitedAt }), capture
}

func TestAnInvitationIsRecordedAndItsLinkSent(t *testing.T) {
	t.Parallel()

	store := &inviteStore{}
	service, capture := invitations(t, store, nil)

	invited, err := service.Create(t.Context(), inviter, "budi@example.test", "contributor")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if invited.Email != "budi@example.test" || invited.Role != "contributor" {
		t.Errorf("Create returned %+v, want the address and role as given", invited)
	}

	if want := invitedAt.Add(7 * 24 * time.Hour); !invited.ExpiresAt.Equal(want) {
		t.Errorf("deadline %v, want %v", invited.ExpiresAt, want)
	}

	delivery, ok := capture.Last()
	if !ok {
		t.Fatal("no message was sent")
	}

	if delivery.Message.To != "budi@example.test" {
		t.Errorf("message went to %q", delivery.Message.To)
	}

	if !strings.Contains(delivery.Message.Text, "https://pm.example.test/invite/") {
		t.Errorf("the message carries no invitation link:\n%s", delivery.Message.Text)
	}
}

// The property the stored digest depends on: what is written to the database
// must be the hash of the token that went out, and the token must exist nowhere
// else. If these ever drift apart no invitation can be redeemed at all.
func TestTheLinkIsTheOnlyCopyOfTheToken(t *testing.T) {
	t.Parallel()

	store := &inviteStore{}
	service, capture := invitations(t, store, nil)

	if _, err := service.Create(t.Context(), inviter, "budi@example.test", "viewer"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	delivery, _ := capture.Last()

	_, after, found := strings.Cut(delivery.Message.Text, "/invite/")
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

	// Nothing handed back to the caller may carry it either.
	typ := reflect.TypeFor[identitysvc.Invited]()
	for i := range typ.NumField() {
		if name := typ.Field(i).Name; strings.Contains(strings.ToLower(name), "token") {
			t.Errorf("Invited has a field %q", name)
		}
	}
}

// Invitations create accounts and nothing else. Somebody who already has one is
// added to a folder or project directly, with no invitation involved.
func TestAnAddressThatAlreadyHasAnAccountIsRefused(t *testing.T) {
	t.Parallel()

	store := &inviteStore{existingAccount: true}
	service, capture := invitations(t, store, nil)

	_, err := service.Create(t.Context(), inviter, "budi@example.test", "contributor")
	if !errors.Is(err, identitysvc.ErrEmailTaken) {
		t.Fatalf("Create error = %v, want ErrEmailTaken", err)
	}

	if len(capture.Sent()) != 0 {
		t.Error("a message was sent for an address that already has an account")
	}

	if slices := store.calls; len(slices) != 1 || slices[0] != "lookup" {
		t.Errorf("the store was asked to do %v, want the lookup alone", slices)
	}
}

func TestARoleThatCannotBeInvitedIsRefused(t *testing.T) {
	t.Parallel()

	for name, role := range map[string]string{
		"owner":       "owner",
		"unknown":     "admin",
		"empty":       "",
		"capitalized": "Contributor",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := &inviteStore{}
			service, capture := invitations(t, store, nil)

			_, err := service.Create(t.Context(), inviter, "budi@example.test", role)
			if !errors.Is(err, identitysvc.ErrRoleNotInvitable) {
				t.Fatalf("Create(role=%q) error = %v, want ErrRoleNotInvitable", role, err)
			}

			if len(capture.Sent()) != 0 || len(store.calls) != 0 {
				t.Error("a refused role still reached the database or the mail server")
			}
		})
	}
}

func TestAnAddressThatIsNotOneIsRefused(t *testing.T) {
	t.Parallel()

	for name, email := range map[string]string{
		"empty":            "",
		"blank":            "   ",
		"not an address":   "budi",
		"with a name":      "Budi <budi@example.test>",
		"two people":       "budi@example.test, ragil@example.test",
		"newline injected": "budi@example.test\r\nBcc: attacker@example.test",
		"far too long":     strings.Repeat("a", 250) + "@example.test",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := &inviteStore{}
			service, capture := invitations(t, store, nil)

			_, err := service.Create(t.Context(), inviter, email, "viewer")
			if !errors.Is(err, identitysvc.ErrInvalidEmail) {
				t.Fatalf("Create(email=%q) error = %v, want ErrInvalidEmail", email, err)
			}

			if len(capture.Sent()) != 0 || len(store.calls) != 0 {
				t.Error("a refused address still reached the database or the mail server")
			}
		})
	}
}

// One address must never have two live links. The order is the claim: closing
// the old one after writing the new one would close the new one too.
func TestTheLinkAlreadySentIsClosedBeforeTheNewOneIsWritten(t *testing.T) {
	t.Parallel()

	store := &inviteStore{}
	service, _ := invitations(t, store, nil)

	if _, err := service.Create(t.Context(), inviter, "budi@example.test", "viewer"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	want := []string{"lookup", "expire", "create"}
	if strings.Join(store.calls, ",") != strings.Join(want, ",") {
		t.Errorf("the store was asked to do %v, want %v", store.calls, want)
	}
}

// docs/nfr.md is explicit: an invitation that could not be delivered is shown
// as a failure, not swallowed. An invitation nobody receives is not one.
func TestADeliveryFailureIsReportedAndNotSwallowed(t *testing.T) {
	t.Parallel()

	store := &inviteStore{}
	service, capture := invitations(t, store, nil)

	capture.Fail(errors.New("the mail server hung up"))

	_, err := service.Create(t.Context(), inviter, "budi@example.test", "viewer")
	if !errors.Is(err, identitysvc.ErrUndeliverable) {
		t.Fatalf("Create error = %v, want ErrUndeliverable", err)
	}
}

// Nothing is sent for an invitation that was not written. The link in a message
// that goes out after a failed write points at a row nobody can redeem.
func TestNothingIsSentWhenTheWriteFails(t *testing.T) {
	t.Parallel()

	failures := map[string]*inviteStore{
		"the insert fails":        {createErr: errors.New("insert failed")},
		"superseding fails":       {expireErr: errors.New("update failed")},
		"the lookup itself fails": {lookupErr: errors.New("select failed")},
	}

	for name, store := range failures {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			service, capture := invitations(t, store, nil)

			if _, err := service.Create(t.Context(), inviter, "budi@example.test", "viewer"); err == nil {
				t.Fatal("Create returned no error although the write failed")
			}

			if len(capture.Sent()) != 0 {
				t.Error("a message was sent for an invitation that was never written")
			}
		})
	}
}

// The password every redemption test uses. Long enough for ADR-0009, which is
// the only rule the policy has.
const chosenPassword = "sandi-yang-cukup-panjang"

// link mints a token and the open invitation that carries its digest, so that
// what the test hands to Accept and what the store answers with cannot drift.
func link(t *testing.T, expiresAt time.Time, acceptedAt *time.Time) (string, *identityrepo.GetInvitationByTokenHashRow) {
	t.Helper()

	token, _, err := identitydom.NewInvitationToken()
	if err != nil {
		t.Fatalf("new invitation token: %v", err)
	}

	row := &identityrepo.GetInvitationByTokenHashRow{
		ID:        "33333333-3333-3333-3333-333333333333",
		Email:     "budi@example.test",
		Role:      "contributor",
		InvitedBy: inviter,
		CreatedAt: pgtype.Timestamptz{Time: invitedAt, Valid: true},
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	}

	if acceptedAt != nil {
		row.AcceptedAt = pgtype.Timestamptz{Time: *acceptedAt, Valid: true}
	}

	// The digest the store would have been keyed by is deliberately not kept:
	// the fake answers with the row it was given, and the test that cares
	// recomputes the hash from the token itself rather than from anything this
	// helper handed it.
	return token, row
}

func TestALinkCreatesTheAccountItWasSentFor(t *testing.T) {
	t.Parallel()

	token, invitation := link(t, invitedAt.Add(7*24*time.Hour), nil)
	store := &inviteStore{invitation: invitation}
	service, _ := invitations(t, store, nil)

	user, err := service.Accept(t.Context(), token, "  Budi Santoso  ", chosenPassword)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}

	// The address and the role are the owner's decision, carried by the
	// invitation. Only the name and the password come from the person clicking.
	if user.Email != "budi@example.test" || user.Role != "contributor" {
		t.Errorf("Accept returned %+v, want the address and role from the invitation", user)
	}

	if user.Name != "Budi Santoso" {
		t.Errorf("name %q, want the surrounding space taken off and nothing else", user.Name)
	}

	if user.Timezone != "Asia/Jakarta" {
		t.Errorf("timezone %q, want the one the database chose", user.Timezone)
	}

	if store.createdUser.Email != invitation.Email || store.createdUser.Role != invitation.Role {
		t.Errorf("the account was written as %+v, want the invitation's address and role", store.createdUser)
	}
}

// The digest is the only thing that connects a link to a row. If the lookup
// ever stopped hashing the token, every invitation would become unredeemable —
// or, far worse, the raw token would be compared against a column of digests.
func TestTheStoredDigestIsWhatTheLinkIsLookedUpBy(t *testing.T) {
	t.Parallel()

	token, invitation := link(t, invitedAt.Add(7*24*time.Hour), nil)
	store := &inviteStore{invitation: invitation}
	service, _ := invitations(t, store, nil)

	if _, err := service.Accept(t.Context(), token, "Budi", chosenPassword); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("the token is not a token: %v", err)
	}

	sum := sha256.Sum256(raw)

	if string(store.lookedUp) != string(sum[:]) {
		t.Error("the invitation was looked up by something other than the hash of the token in the link")
	}
}

// The password is hashed, and the hash is what the account is written with.
func TestThePasswordIsHashedBeforeItIsStored(t *testing.T) {
	t.Parallel()

	token, invitation := link(t, invitedAt.Add(7*24*time.Hour), nil)
	store := &inviteStore{invitation: invitation}
	service, _ := invitations(t, store, nil)

	if _, err := service.Accept(t.Context(), token, "Budi", chosenPassword); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	if strings.Contains(store.createdUser.PasswordHash, chosenPassword) {
		t.Fatal("the password reached the database")
	}

	if err := identitydom.VerifyPassword(store.createdUser.PasswordHash, chosenPassword); err != nil {
		t.Errorf("the stored hash does not verify the password that was chosen: %v", err)
	}
}

// Four different facts, one answer. Telling them apart would confirm to
// whoever holds a link that the address was invited — the enumeration
// docs/threat-model.md closes on this path.
func TestALinkThatCannotBeUsedIsRefusedTheSameWay(t *testing.T) {
	t.Parallel()

	expired, expiredRow := link(t, invitedAt.Add(-time.Hour), nil)
	accepted, acceptedRow := link(t, invitedAt.Add(7*24*time.Hour), &invitedAt)
	unknown, _ := link(t, invitedAt.Add(7*24*time.Hour), nil)
	lost, lostRow := link(t, invitedAt.Add(7*24*time.Hour), nil)

	cases := map[string]struct {
		token string
		store *inviteStore
	}{
		"malformed":        {token: "not-a-token", store: &inviteStore{}},
		"never issued":     {token: unknown, store: &inviteStore{}},
		"expired":          {token: expired, store: &inviteStore{invitation: expiredRow}},
		"already redeemed": {token: accepted, store: &inviteStore{invitation: acceptedRow}},
		"lost the race":    {token: lost, store: &inviteStore{invitation: lostRow, lostRace: true}},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			service, _ := invitations(t, tc.store, nil)

			_, err := service.Accept(t.Context(), tc.token, "Budi", chosenPassword)
			if !errors.Is(err, identitysvc.ErrInvitationNotUsable) {
				t.Fatalf("Accept error = %v, want ErrInvitationNotUsable", err)
			}

			if slices.Contains(tc.store.calls, "account") {
				t.Error("an account was created from a link that cannot be used")
			}
		})
	}
}

// The claim the single-use property rests on: the stamp comes first, and a
// stamp that updates no rows stops the account from being written at all.
func TestTheInvitationIsStampedBeforeTheAccountIsWritten(t *testing.T) {
	t.Parallel()

	token, invitation := link(t, invitedAt.Add(7*24*time.Hour), nil)
	store := &inviteStore{invitation: invitation}
	service, _ := invitations(t, store, nil)

	if _, err := service.Accept(t.Context(), token, "Budi", chosenPassword); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	want := []string{"read", "stamp", "account"}
	if strings.Join(store.calls, ",") != strings.Join(want, ",") {
		t.Errorf("the store was asked to do %v, want %v", store.calls, want)
	}
}

// The unique index is what decides an address has one account, and this is what
// it looks like when it fires: two invitations to one address redeemed at the
// same moment, or an account created by some other route in between.
func TestAnAddressThatGainedAnAccountMeanwhileIsRefused(t *testing.T) {
	t.Parallel()

	token, invitation := link(t, invitedAt.Add(7*24*time.Hour), nil)
	store := &inviteStore{
		invitation:    invitation,
		createUserErr: &pgconn.PgError{Code: "23505", ConstraintName: "users_email_key"},
	}
	service, _ := invitations(t, store, nil)

	_, err := service.Accept(t.Context(), token, "Budi", chosenPassword)
	if !errors.Is(err, identitysvc.ErrEmailTaken) {
		t.Fatalf("Accept error = %v, want ErrEmailTaken", err)
	}
}

// Any other database failure is an internal one, and must not be dressed up as
// a taken address — that would tell somebody their invitation was spent when
// the database merely fell over.
func TestADatabaseFailureIsNotReportedAsATakenAddress(t *testing.T) {
	t.Parallel()

	failures := map[string]*inviteStore{
		"the read fails":   {invitationErr: errors.New("select failed")},
		"the stamp fails":  {stampErr: errors.New("update failed")},
		"the insert fails": {createUserErr: errors.New("insert failed")},
	}

	for name, store := range failures {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			token, invitation := link(t, invitedAt.Add(7*24*time.Hour), nil)
			store.invitation = invitation

			service, _ := invitations(t, store, nil)

			_, err := service.Accept(t.Context(), token, "Budi", chosenPassword)
			if err == nil {
				t.Fatal("Accept returned no error although the store failed")
			}

			if errors.Is(err, identitysvc.ErrEmailTaken) || errors.Is(err, identitysvc.ErrInvitationNotUsable) {
				t.Errorf("a store failure was reported as %v", err)
			}
		})
	}
}

// The password policy is checked before the link is, so a password that breaks
// it costs no database work — and, more to the point, does not spend the link.
func TestAPasswordThatBreaksThePolicyDoesNotSpendTheLink(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		password string
		want     error
	}{
		"too short": {password: "pendek", want: identitydom.ErrPasswordTooShort},
		"too long":  {password: strings.Repeat("a", 1025), want: identitydom.ErrPasswordTooLong},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			token, invitation := link(t, invitedAt.Add(7*24*time.Hour), nil)
			store := &inviteStore{invitation: invitation}
			service, _ := invitations(t, store, nil)

			_, err := service.Accept(t.Context(), token, "Budi", tc.password)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Accept error = %v, want %v", err, tc.want)
			}

			if len(store.calls) != 0 {
				t.Errorf("a refused password still reached the database: %v", store.calls)
			}
		})
	}
}

func TestANameThatCannotBeStoredIsRefused(t *testing.T) {
	t.Parallel()

	for name, chosen := range map[string]string{
		"empty":    "",
		"blank":    "   \t\n ",
		"too long": strings.Repeat("a", 201),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			token, invitation := link(t, invitedAt.Add(7*24*time.Hour), nil)
			store := &inviteStore{invitation: invitation}
			service, _ := invitations(t, store, nil)

			_, err := service.Accept(t.Context(), token, chosen, chosenPassword)
			if !errors.Is(err, identitysvc.ErrInvalidName) {
				t.Fatalf("Accept(name=%q) error = %v, want ErrInvalidName", chosen, err)
			}

			if len(store.calls) != 0 {
				t.Errorf("a refused name still reached the database: %v", store.calls)
			}
		})
	}
}

// A transaction that could not commit created nothing, so the caller must not
// be handed an account.
func TestNoAccountIsReturnedWhenTheTransactionCannotCommit(t *testing.T) {
	t.Parallel()

	token, invitation := link(t, invitedAt.Add(7*24*time.Hour), nil)
	store := &inviteStore{invitation: invitation}
	service, _ := invitations(t, store, errors.New("commit failed"))

	user, err := service.Accept(t.Context(), token, "Budi", chosenPassword)
	if err == nil {
		t.Fatal("Accept returned no error although the commit failed")
	}

	if user.ID != "" {
		t.Errorf("Accept returned %+v for a transaction that never committed", user)
	}
}

// The send happens after the commit, so a transaction that could not commit
// must not have produced a message either.
func TestNothingIsSentWhenTheTransactionCannotCommit(t *testing.T) {
	t.Parallel()

	store := &inviteStore{}
	service, capture := invitations(t, store, errors.New("commit failed"))

	if _, err := service.Create(t.Context(), inviter, "budi@example.test", "viewer"); err == nil {
		t.Fatal("Create returned no error although the commit failed")
	}

	if len(capture.Sent()) != 0 {
		t.Error("a message was sent for a transaction that never committed")
	}
}
