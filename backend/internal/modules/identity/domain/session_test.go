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

// The cookie value and the stored digest have to be two different things, or
// a database dump is a pile of usable sessions.
func TestTheStoredDigestIsNotTheTokenItself(t *testing.T) {
	t.Parallel()

	token, digest, err := identitydom.NewSessionToken()
	if err != nil {
		t.Fatalf("NewSessionToken: %v", err)
	}

	if strings.Contains(token, base64.RawURLEncoding.EncodeToString(digest)) {
		t.Error("the digest can be read out of the token")
	}

	if len(digest) != sha256.Size {
		t.Errorf("digest is %d bytes, want %d — sessions.token_hash has a CHECK on this", len(digest), sha256.Size)
	}

	// The digest a client's token hashes to must be the one that was stored,
	// or no session ever looks up.
	looked, err := identitydom.SessionTokenDigest(token)
	if err != nil {
		t.Fatalf("SessionTokenDigest of a token we just issued: %v", err)
	}

	if !bytes.Equal(looked, digest) {
		t.Error("hashing the issued token does not reproduce the stored digest")
	}
}

func TestEveryTokenIsDifferent(t *testing.T) {
	t.Parallel()

	const issues = 1000

	seen := make(map[string]struct{}, issues)
	for i := range issues {
		token, _, err := identitydom.NewSessionToken()
		if err != nil {
			t.Fatalf("NewSessionToken %d: %v", i, err)
		}

		if _, repeat := seen[token]; repeat {
			t.Fatalf("token %d had already been issued", i)
		}

		seen[token] = struct{}{}
	}
}

// A cookie carries whatever the client puts in it. None of this is worth a
// database round trip.
func TestAValueThatCannotBeATokenIsRefusedBeforeAnyLookup(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		presented string
	}{
		{"an empty cookie", ""},
		{"not base64url at all", "not a token"},
		{"standard base64 with padding", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))},
		{"base64url of too few bytes", base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 31))},
		{"base64url of too many bytes", base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 33))},
		{"a megabyte of base64", base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 1<<20))},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			digest, err := identitydom.SessionTokenDigest(c.presented)
			if !errors.Is(err, identitydom.ErrSessionTokenMalformed) {
				t.Errorf("SessionTokenDigest = %x, %v; want ErrSessionTokenMalformed", digest, err)
			}
		})
	}
}

func TestASessionIsUsableUntilItsDeadlineAndNotAfter(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 8, 7, 9, 0, 0, 0, time.UTC)
	session := identitydom.Session{
		CreatedAt: created,
		ExpiresAt: identitydom.NewSessionExpiry(created),
	}

	cases := []struct {
		name    string
		now     time.Time
		expired bool
	}{
		{"the moment it was created", created, false},
		{"a day later", created.Add(24 * time.Hour), false},
		{"one second before the deadline", session.ExpiresAt.Add(-time.Second), false},
		{"exactly at the deadline", session.ExpiresAt, true},
		{"a second after", session.ExpiresAt.Add(time.Second), true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := session.IsExpired(c.now); got != c.expired {
				t.Errorf("IsExpired at %v = %v, want %v", c.now, got, c.expired)
			}
		})
	}
}

// The idle window slides, the absolute one does not. A session used every day
// still ends ninety days after it began.
func TestRenewalSlidesTheIdleWindowButNeverPastTheAbsoluteOne(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 8, 7, 9, 0, 0, 0, time.UTC)
	session := identitydom.Session{
		CreatedAt: created,
		ExpiresAt: identitydom.NewSessionExpiry(created),
	}

	absolute := created.Add(identitydom.SessionAbsoluteWindow)

	cases := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{"early on, the full idle window", created.Add(time.Hour), created.Add(time.Hour + identitydom.SessionIdleWindow)},
		{"near the ceiling, only what is left", absolute.Add(-24 * time.Hour), absolute},
		{"at the ceiling, nothing more", absolute, absolute},
		{"past the ceiling, still nothing more", absolute.Add(24 * time.Hour), absolute},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := session.RenewedExpiry(c.now); !got.Equal(c.want) {
				t.Errorf("RenewedExpiry at %v = %v, want %v", c.now, got, c.want)
			}
		})
	}
}

// Sliding on every request would be one UPDATE per request for the whole
// application. The floor is what stops a card drag from costing a session
// write.
func TestASessionIsOnlyWrittenBackWhenTheDeadlineMovesEnough(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 8, 7, 9, 0, 0, 0, time.UTC)
	session := identitydom.Session{
		CreatedAt: created,
		ExpiresAt: identitydom.NewSessionExpiry(created),
	}

	cases := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"a second after it was issued", created.Add(time.Second), false},
		{"a minute after", created.Add(time.Minute), false},
		{"an hour after, exactly at the floor", created.Add(time.Hour), false},
		{"an hour and a second after", created.Add(time.Hour + time.Second), true},
		{"a week after", created.Add(7 * 24 * time.Hour), true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := session.NeedsRenewal(c.now); got != c.want {
				t.Errorf("NeedsRenewal at %v = %v, want %v", c.now, got, c.want)
			}
		})
	}
}

// A session that has reached its ceiling has nowhere left to slide, so its
// last day must cost no writes at all.
func TestASessionAtItsCeilingStopsAskingToBeWritten(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 8, 7, 9, 0, 0, 0, time.UTC)
	absolute := created.Add(identitydom.SessionAbsoluteWindow)

	session := identitydom.Session{
		CreatedAt: created,
		ExpiresAt: absolute,
	}

	for _, now := range []time.Time{
		absolute.Add(-24 * time.Hour),
		absolute.Add(-time.Hour),
		absolute.Add(-time.Second),
	} {
		if session.NeedsRenewal(now) {
			t.Errorf("a session already at its ceiling asked to be written at %v", now)
		}
	}
}
