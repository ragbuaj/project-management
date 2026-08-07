package domain_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	identitydom "github.com/ragbuaj/project-management/backend/internal/modules/identity/domain"
)

// The link that goes out by e-mail and the row left behind have to be two
// different things, or a database dump is a pile of usable invitations — and
// every one of them mints an account.
func TestTheStoredInvitationDigestIsNotTheTokenItself(t *testing.T) {
	t.Parallel()

	token, digest, err := identitydom.NewInvitationToken()
	if err != nil {
		t.Fatalf("NewInvitationToken: %v", err)
	}

	if strings.Contains(token, base64.RawURLEncoding.EncodeToString(digest)) {
		t.Error("the digest can be read out of the token")
	}

	if len(digest) != sha256.Size {
		t.Errorf("digest is %d bytes, want %d — invitations.token_hash has a CHECK on this", len(digest), sha256.Size)
	}

	looked, err := identitydom.InvitationTokenDigest(token)
	if err != nil {
		t.Fatalf("InvitationTokenDigest of a token we just issued: %v", err)
	}

	if !bytes.Equal(looked, digest) {
		t.Error("hashing the issued token does not reproduce the stored digest")
	}
}

func TestEveryInvitationTokenIsDifferent(t *testing.T) {
	t.Parallel()

	const issues = 1000

	seen := make(map[string]struct{}, issues)

	for i := range issues {
		token, _, err := identitydom.NewInvitationToken()
		if err != nil {
			t.Fatalf("NewInvitationToken %d: %v", i, err)
		}

		if _, repeat := seen[token]; repeat {
			t.Fatalf("token %d had already been issued", i)
		}

		seen[token] = struct{}{}
	}
}

// A link carries whatever is in the URL bar, including what an e-mail client
// did to it on the way. None of this is worth a database round trip.
func TestAValueThatCannotBeAnInvitationTokenIsRefusedBeforeAnyLookup(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		presented string
	}{
		{"an empty link", ""},
		{"not base64url at all", "not a token"},
		{"standard base64 with padding", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))},
		{"base64url of too few bytes", base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 31))},
		{"base64url of too many bytes", base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 33))},
		{"a megabyte of base64", base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 1<<20))},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			digest, err := identitydom.InvitationTokenDigest(c.presented)
			if !errors.Is(err, identitydom.ErrInvitationTokenMalformed) {
				t.Errorf("InvitationTokenDigest = %x, %v; want ErrInvitationTokenMalformed", digest, err)
			}
		})
	}
}

func TestAnInvitationIsUsableUntilItsDeadlineAndNotAfter(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 8, 9, 0, 0, 0, time.UTC)
	invitation := identitydom.Invitation{ExpiresAt: identitydom.NewInvitationExpiry(now)}

	cases := []struct {
		name       string
		at         time.Time
		wantUsable bool
	}{
		{"the moment it was issued", now, true},
		{"a day later", now.Add(24 * time.Hour), true},
		{"a moment before the deadline", invitation.ExpiresAt.Add(-time.Nanosecond), true},
		// The deadline itself is out. An invitation usable "until" a moment must
		// not still work at that moment, or every window is one tick longer than
		// it says.
		{"the deadline itself", invitation.ExpiresAt, false},
		{"a moment after", invitation.ExpiresAt.Add(time.Nanosecond), false},
		{"a month later", now.Add(30 * 24 * time.Hour), false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := invitation.IsUsable(c.at); got != c.wantUsable {
				t.Errorf("IsUsable(%v) = %t, want %t", c.at, got, c.wantUsable)
			}

			if got := invitation.IsExpired(c.at); got == c.wantUsable {
				t.Errorf("IsExpired(%v) = %t, which contradicts IsUsable", c.at, got)
			}
		})
	}
}

// The property that makes an invitation single-use. Without it a link that has
// already created one account creates a second one, and the second account is
// one nobody decided to create.
func TestAnAcceptedInvitationIsRefusedEvenWithTimeLeft(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 8, 9, 0, 0, 0, time.UTC)
	accepted := now.Add(time.Minute)

	invitation := identitydom.Invitation{
		ExpiresAt:  identitydom.NewInvitationExpiry(now),
		AcceptedAt: &accepted,
	}

	if invitation.IsExpired(now) {
		t.Fatal("this invitation is meant to still have time left")
	}

	if !invitation.IsAccepted() {
		t.Error("IsAccepted = false for an invitation with an accepted_at")
	}

	if invitation.IsUsable(now) {
		t.Error("an already accepted invitation is still usable")
	}
}

func TestAnOpenInvitationIsNotAccepted(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 8, 9, 0, 0, 0, time.UTC)
	invitation := identitydom.Invitation{ExpiresAt: identitydom.NewInvitationExpiry(now)}

	if invitation.IsAccepted() {
		t.Error("IsAccepted = true for an invitation nobody has redeemed")
	}
}

// The window is stated in the schema as a deadline, so the only place it is
// decided is here.
func TestTheInvitationWindowIsSevenDays(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 8, 9, 0, 0, 0, time.UTC)

	if got, want := identitydom.NewInvitationExpiry(now), now.Add(7*24*time.Hour); !got.Equal(want) {
		t.Errorf("NewInvitationExpiry = %v, want %v", got, want)
	}
}
