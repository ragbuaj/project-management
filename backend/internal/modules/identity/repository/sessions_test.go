package repository_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	identitydom "github.com/ragbuaj/project-management/backend/internal/modules/identity/domain"
	identityrepo "github.com/ragbuaj/project-management/backend/internal/modules/identity/repository"
)

// issueSession creates a session for user and hands back the digest it is
// found by, which is all a caller ever holds after the response is written.
func issueSession(t *testing.T, ctx context.Context, q *identityrepo.Queries, user string, expires time.Time) []byte {
	t.Helper()

	_, digest, err := identitydom.NewSessionToken()
	if err != nil {
		t.Fatalf("NewSessionToken(): %v", err)
	}

	if _, err := q.CreateSession(ctx, identityrepo.CreateSessionParams{
		UserID:    user,
		TokenHash: digest,
		UserAgent: "Mozilla/5.0 (test)",
		ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
	}); err != nil {
		t.Fatalf("CreateSession(): %v", err)
	}

	return digest
}

// The lookup that runs on every authenticated request. It returns the user
// alongside the session because the two are always needed together, and two
// round trips per request for that is a cost paid on every page.
func TestASessionIsFoundByItsDigestAndBringsItsUserAlong(t *testing.T) {
	ctx, _, q := queriesTx(t)

	user := createUser(t, ctx, q, "session-owner@example.test", "Session Owner")
	expires := time.Now().Add(identitydom.SessionIdleWindow).UTC().Truncate(time.Microsecond)

	digest := issueSession(t, ctx, q, user, expires)

	found, err := q.GetSessionByTokenHash(ctx, digest)
	if err != nil {
		t.Fatalf("GetSessionByTokenHash(): %v", err)
	}

	if found.UserID != user {
		t.Errorf("session belongs to %s, want %s", found.UserID, user)
	}

	if found.Email != "session-owner@example.test" || found.Name != "Session Owner" {
		t.Errorf("lookup returned %q / %q, want the session owner's address and name", found.Email, found.Name)
	}

	if found.IsOwner {
		t.Error("lookup reported the account as the project owner, which it is not")
	}

	if !found.ExpiresAt.Time.Equal(expires) {
		t.Errorf("expires_at came back as %v, want %v", found.ExpiresAt.Time, expires)
	}

	// A digest nobody issued must find nothing rather than the nearest row.
	other := bytes.Repeat([]byte{7}, 32)
	if _, err := q.GetSessionByTokenHash(ctx, other); !isNoRows(err) {
		t.Errorf("GetSessionByTokenHash() with an unissued digest returned %v, want no rows", err)
	}
}

// The reason the lookup joins users_live rather than users. Masking an account
// has to stop its sessions from authenticating at once — not once a sweep gets
// around to deleting them.
func TestASoftDeletedUsersSessionStopsAuthenticating(t *testing.T) {
	ctx, _, q := queriesTx(t)

	user := createUser(t, ctx, q, "leaving@example.test", "Leaving")
	digest := issueSession(t, ctx, q, user, time.Now().Add(identitydom.SessionIdleWindow))

	if _, err := q.GetSessionByTokenHash(ctx, digest); err != nil {
		t.Fatalf("the session does not work before the account is masked: %v", err)
	}

	if _, err := q.SoftDeleteUser(ctx, user); err != nil {
		t.Fatalf("SoftDeleteUser(): %v", err)
	}

	if _, err := q.GetSessionByTokenHash(ctx, digest); !isNoRows(err) {
		t.Errorf("a masked account's session still authenticates: %v", err)
	}
}

// Sliding renewal has to move the deadline the caller computed, not one the
// database invents, or the absolute ninety-day ceiling means nothing.
func TestTouchingASessionMovesTheDeadlineTheCallerAsksFor(t *testing.T) {
	ctx, _, q := queriesTx(t)

	user := createUser(t, ctx, q, "returning@example.test", "Returning")
	digest := issueSession(t, ctx, q, user, time.Now().Add(time.Hour))

	before, err := q.GetSessionByTokenHash(ctx, digest)
	if err != nil {
		t.Fatalf("GetSessionByTokenHash(): %v", err)
	}

	renewed := identitydom.Session{
		CreatedAt: before.CreatedAt.Time,
		ExpiresAt: before.ExpiresAt.Time,
	}.RenewedExpiry(time.Now())

	rows, err := q.TouchSession(ctx, identityrepo.TouchSessionParams{
		ID:        before.ID,
		ExpiresAt: pgtype.Timestamptz{Time: renewed, Valid: true},
	})
	if err != nil {
		t.Fatalf("TouchSession(): %v", err)
	}

	if rows != 1 {
		t.Fatalf("TouchSession() touched %d rows, want 1", rows)
	}

	after, err := q.GetSessionByTokenHash(ctx, digest)
	if err != nil {
		t.Fatalf("GetSessionByTokenHash(): %v", err)
	}

	if !after.ExpiresAt.Time.Equal(renewed.UTC().Truncate(time.Microsecond)) {
		t.Errorf("expires_at is %v after the touch, want %v", after.ExpiresAt.Time, renewed)
	}

	if !after.LastSeenAt.Time.After(before.LastSeenAt.Time) {
		t.Errorf("last_seen_at did not move: %v then %v", before.LastSeenAt.Time, after.LastSeenAt.Time)
	}
}

// Logout. ADR-0005 chose opaque sessions so that this is the whole mechanism:
// the row is gone, and there is no list anywhere still saying otherwise.
func TestDeletingASessionEndsThatSessionAndOnlyThatOne(t *testing.T) {
	ctx, _, q := queriesTx(t)

	user := createUser(t, ctx, q, "two-devices@example.test", "Two Devices")
	laptop := issueSession(t, ctx, q, user, time.Now().Add(identitydom.SessionIdleWindow))
	phone := issueSession(t, ctx, q, user, time.Now().Add(identitydom.SessionIdleWindow))

	rows, err := q.DeleteSessionByTokenHash(ctx, laptop)
	if err != nil {
		t.Fatalf("DeleteSessionByTokenHash(): %v", err)
	}

	if rows != 1 {
		t.Fatalf("DeleteSessionByTokenHash() removed %d rows, want 1", rows)
	}

	if _, err := q.GetSessionByTokenHash(ctx, laptop); !isNoRows(err) {
		t.Errorf("the signed-out session still resolves: %v", err)
	}

	if _, err := q.GetSessionByTokenHash(ctx, phone); err != nil {
		t.Errorf("signing out on one device ended the session on the other: %v", err)
	}

	// Logging out twice — a double-clicked button, a retried request — is not
	// an error, it is a request that finds nothing left to do.
	rows, err = q.DeleteSessionByTokenHash(ctx, laptop)
	if err != nil {
		t.Fatalf("DeleteSessionByTokenHash() a second time: %v", err)
	}

	if rows != 0 {
		t.Errorf("a repeated logout removed %d rows, want 0", rows)
	}
}
