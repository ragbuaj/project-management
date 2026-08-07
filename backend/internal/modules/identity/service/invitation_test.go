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
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ragbuaj/project-management/backend/internal/mail"
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

func invitations(t *testing.T, store *inviteStore, commitErr error) (*identitysvc.Invitations, *mail.Capture) {
	t.Helper()

	capture := mail.NewCapture(sentFrom)

	base, err := url.Parse("https://pm.example.test")
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}

	inTx := func(_ context.Context, fn func(identitysvc.InvitationStore) error) error {
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
