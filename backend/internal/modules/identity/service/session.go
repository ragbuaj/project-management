// Package service holds the identity business rules and owns the transaction
// boundaries around them (ADR-0008).
package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	identitydom "github.com/ragbuaj/project-management/backend/internal/modules/identity/domain"
	identityrepo "github.com/ragbuaj/project-management/backend/internal/modules/identity/repository"
)

// SessionStore is the part of the repository this service uses. It is declared
// here, at the consumer, so the service depends on the operations it needs
// rather than on everything the generated Queries type happens to expose.
type SessionStore interface {
	CreateSession(ctx context.Context, arg identityrepo.CreateSessionParams) (identityrepo.CreateSessionRow, error)
	DeleteSessionByTokenHash(ctx context.Context, tokenHash []byte) (int64, error)
}

// maxUserAgentLength bounds what is written to sessions.user_agent. The header
// is attacker-controlled and unbounded; the column is text, which in
// PostgreSQL means the row would be as long as the request cared to make it.
const maxUserAgentLength = 512

// Sessions issues and ends browser sessions.
type Sessions struct {
	store SessionStore
	log   *slog.Logger

	// now is injected rather than called directly so the deadlines a session
	// is built with are testable without waiting fourteen days.
	now func() time.Time
}

// NewSessions wires the service. The clock is explicit because a service that
// reaches for time.Now on its own cannot be tested at the boundaries that
// matter, and those boundaries are the whole point of a session.
func NewSessions(store SessionStore, log *slog.Logger, now func() time.Time) *Sessions {
	return &Sessions{store: store, log: log, now: now}
}

// Issue creates a session and returns the token to put in the cookie. The
// token is the only copy: what is stored is its digest, and this is the last
// moment the value exists anywhere.
func (s *Sessions) Issue(ctx context.Context, userID, userAgent string) (string, identitydom.Session, error) {
	token, digest, err := identitydom.NewSessionToken()
	if err != nil {
		return "", identitydom.Session{}, fmt.Errorf("new session token: %w", err)
	}

	row, err := s.store.CreateSession(ctx, identityrepo.CreateSessionParams{
		UserID:    userID,
		TokenHash: digest,
		UserAgent: cleanUserAgent(userAgent),
		ExpiresAt: pgtype.Timestamptz{Time: identitydom.NewSessionExpiry(s.now()), Valid: true},
	})
	if err != nil {
		return "", identitydom.Session{}, fmt.Errorf("create session: %w", err)
	}

	return token, identitydom.Session{
		ID:         row.ID,
		UserID:     row.UserID,
		CreatedAt:  row.CreatedAt.Time,
		LastSeenAt: row.LastSeenAt.Time,
		ExpiresAt:  row.ExpiresAt.Time,
	}, nil
}

// Revoke ends a session. Presenting something that was never a token, or a
// token whose session is already gone, is not an error: logging out twice is a
// double-clicked button, and the caller ends up signed out either way.
func (s *Sessions) Revoke(ctx context.Context, token string) error {
	// A value that was never a token has no row behind it, so there is nothing
	// to delete and nothing to report. It falls through to the same success as
	// a session that was deleted.
	if digest, err := identitydom.SessionTokenDigest(token); err == nil {
		if _, err := s.store.DeleteSessionByTokenHash(ctx, digest); err != nil {
			return fmt.Errorf("delete session: %w", err)
		}
	}

	return nil
}

// cleanUserAgent bounds the header and drops anything that is not valid UTF-8.
//
// Both halves are needed. PostgreSQL rejects invalid UTF-8 in a text column
// outright, so an agent string with a stray byte would fail the INSERT and
// therefore the login — a client could refuse to be logged in, which is
// harmless, or discover it can make any login fail, which is not.
func cleanUserAgent(agent string) string {
	if len(agent) > maxUserAgentLength {
		agent = agent[:maxUserAgentLength]
	}

	// Truncation can cut a rune in half, so this runs second.
	return strings.ToValidUTF8(agent, "")
}
