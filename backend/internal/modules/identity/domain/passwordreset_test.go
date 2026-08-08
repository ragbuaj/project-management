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
// different things, or a database dump is a pile of usable reset links — and
// every one of them opens an account that already has work in it.
func TestTheStoredPasswordResetDigestIsNotTheTokenItself(t *testing.T) {
	t.Parallel()

	token, digest, err := identitydom.NewPasswordResetToken()
	if err != nil {
		t.Fatalf("NewPasswordResetToken: %v", err)
	}

	if strings.Contains(token, base64.RawURLEncoding.EncodeToString(digest)) {
		t.Error("the digest can be read out of the token")
	}

	if len(digest) != sha256.Size {
		t.Errorf("digest is %d bytes, want %d — password_resets.token_hash has a CHECK on this", len(digest), sha256.Size)
	}

	looked, err := identitydom.PasswordResetTokenDigest(token)
	if err != nil {
		t.Fatalf("PasswordResetTokenDigest of a token we just issued: %v", err)
	}

	if !bytes.Equal(looked, digest) {
		t.Error("hashing the issued token does not reproduce the stored digest")
	}
}

func TestEveryPasswordResetTokenIsDifferent(t *testing.T) {
	t.Parallel()

	const issues = 1000

	seen := make(map[string]struct{}, issues)

	for i := range issues {
		token, _, err := identitydom.NewPasswordResetToken()
		if err != nil {
			t.Fatalf("NewPasswordResetToken %d: %v", i, err)
		}

		if _, repeat := seen[token]; repeat {
			t.Fatalf("token %d had already been issued", i)
		}

		seen[token] = struct{}{}
	}
}

// A reset link and an invitation link are the same 32 bytes, and that is on
// purpose — but a token minted for one must not be presented as the other. What
// keeps them apart is the column each digest is looked up in, so the digests
// themselves have to agree byte for byte across both.
func TestAResetTokenHashesTheSameWayAnInvitationTokenDoes(t *testing.T) {
	t.Parallel()

	token, digest, err := identitydom.NewPasswordResetToken()
	if err != nil {
		t.Fatalf("NewPasswordResetToken: %v", err)
	}

	asInvitation, err := identitydom.InvitationTokenDigest(token)
	if err != nil {
		t.Fatalf("InvitationTokenDigest: %v", err)
	}

	if !bytes.Equal(asInvitation, digest) {
		t.Error("the two token families hash differently, so one row's digest could never be found by the other's lookup")
	}
}

// A link carries whatever is in the URL bar, including what a mail client did
// to it on the way. None of this is worth a database round trip.
func TestAValueThatCannotBeAPasswordResetTokenIsRefusedBeforeAnyLookup(t *testing.T) {
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

			digest, err := identitydom.PasswordResetTokenDigest(c.presented)
			if !errors.Is(err, identitydom.ErrPasswordResetTokenMalformed) {
				t.Errorf("PasswordResetTokenDigest = %x, %v; want ErrPasswordResetTokenMalformed", digest, err)
			}
		})
	}
}

func TestAPasswordResetIsUsableUntilItsDeadlineAndNotAfter(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 8, 9, 0, 0, 0, time.UTC)
	reset := identitydom.PasswordReset{ExpiresAt: identitydom.NewPasswordResetExpiry(now)}

	cases := []struct {
		name       string
		at         time.Time
		wantUsable bool
	}{
		{"the moment it was issued", now, true},
		{"half an hour later", now.Add(30 * time.Minute), true},
		{"a moment before the deadline", reset.ExpiresAt.Add(-time.Nanosecond), true},
		// The deadline itself is out. A link usable "until" a moment must not
		// still work at that moment, or every window is one tick longer than it
		// says.
		{"the deadline itself", reset.ExpiresAt, false},
		{"a moment after", reset.ExpiresAt.Add(time.Nanosecond), false},
		{"a day later", now.Add(24 * time.Hour), false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := reset.IsUsable(c.at); got != c.wantUsable {
				t.Errorf("IsUsable(%v) = %t, want %t", c.at, got, c.wantUsable)
			}

			if got := reset.IsExpired(c.at); got == c.wantUsable {
				t.Errorf("IsExpired(%v) = %t, which contradicts IsUsable", c.at, got)
			}
		})
	}
}

// The property that makes a reset single-use. Without it a link that has
// already set one password sets a second one, and whoever kept a copy of the
// mail owns the account for as long as the deadline lasts.
func TestAUsedPasswordResetIsRefusedEvenWithTimeLeft(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 8, 9, 0, 0, 0, time.UTC)
	used := now.Add(time.Minute)

	reset := identitydom.PasswordReset{
		ExpiresAt: identitydom.NewPasswordResetExpiry(now),
		UsedAt:    &used,
	}

	if reset.IsExpired(now) {
		t.Fatal("this reset is meant to still have time left")
	}

	if !reset.IsUsed() {
		t.Error("IsUsed = false for a reset with a used_at")
	}

	if reset.IsUsable(now) {
		t.Error("an already used reset is still usable")
	}
}

func TestAnOpenPasswordResetIsNotUsed(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 8, 9, 0, 0, 0, time.UTC)
	reset := identitydom.PasswordReset{ExpiresAt: identitydom.NewPasswordResetExpiry(now)}

	if reset.IsUsed() {
		t.Error("IsUsed = true for a reset nobody has confirmed")
	}
}

// The window is stated in the schema as a deadline, so the only place it is
// decided is here — and it is deliberately far shorter than the invitation's.
func TestThePasswordResetWindowIsOneHour(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 8, 9, 0, 0, 0, time.UTC)

	if got, want := identitydom.NewPasswordResetExpiry(now), now.Add(time.Hour); !got.Equal(want) {
		t.Errorf("NewPasswordResetExpiry = %v, want %v", got, want)
	}

	if identitydom.PasswordResetWindow >= identitydom.InvitationWindow {
		t.Errorf("the reset window (%v) is not shorter than the invitation window (%v); a link into an account that already holds work must not outlive one that only creates an empty account",
			identitydom.PasswordResetWindow, identitydom.InvitationWindow)
	}
}
