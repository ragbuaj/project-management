// Package service holds the identity business rules and owns the transaction
// boundaries around them (ADR-0008).
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	identitydom "github.com/ragbuaj/project-management/backend/internal/modules/identity/domain"
	identityrepo "github.com/ragbuaj/project-management/backend/internal/modules/identity/repository"
)

// SessionStore is the part of the repository this service uses. It is declared
// here, at the consumer, so the service depends on the operations it needs
// rather than on everything the generated Queries type happens to expose.
type SessionStore interface {
	CreateSession(ctx context.Context, arg identityrepo.CreateSessionParams) (identityrepo.CreateSessionRow, error)
	GetSessionByTokenHash(ctx context.Context, tokenHash []byte) (identityrepo.GetSessionByTokenHashRow, error)
	TouchSession(ctx context.Context, arg identityrepo.TouchSessionParams) (int64, error)
	DeleteSessionByTokenHash(ctx context.Context, tokenHash []byte) (int64, error)
}

// ErrUnauthenticated is the single answer to every way of not being signed in:
// no cookie, a cookie that was never a token, a session that was deleted, a
// session that expired, an account that was masked since.
//
// They are deliberately one error. The log can tell them apart — Authenticate
// records which it was — but a client that can tell "no such session" from
// "expired" learns whether a stolen token was ever real.
var ErrUnauthenticated = errors.New("not authenticated")

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

// Authenticated is what a request learns about its caller. The identity is
// read from the database on every request rather than carried in the token —
// that is the property ADR-0005 chose opaque sessions for, and it is what
// makes a change of role take effect on the next request instead of at the
// next expiry.
type Authenticated struct {
	SessionID string
	UserID    string
	Email     string
	Name      string
	IsOwner   bool
}

// Authenticate resolves a token into its caller, sliding the deadline when
// that is worth a write.
func (s *Sessions) Authenticate(ctx context.Context, token string) (Authenticated, error) {
	digest, err := identitydom.SessionTokenDigest(token)
	if err != nil {
		// Not logged. A malformed cookie is what every crawler and every stale
		// browser tab produces, and a line per request is a log nobody reads.
		return Authenticated{}, ErrUnauthenticated
	}

	row, err := s.store.GetSessionByTokenHash(ctx, digest)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Authenticated{}, ErrUnauthenticated
		}

		return Authenticated{}, fmt.Errorf("get session: %w", err)
	}

	session := identitydom.Session{
		ID:         row.ID,
		UserID:     row.UserID,
		CreatedAt:  row.CreatedAt.Time,
		LastSeenAt: row.LastSeenAt.Time,
		ExpiresAt:  row.ExpiresAt.Time,
	}

	now := s.now()
	if session.IsExpired(now) {
		// Left in place rather than deleted here: this is a read path, and the
		// sweep that removes expired rows is a job. Deleting on read would put
		// a write behind every request carrying an old cookie.
		s.log.InfoContext(ctx, "session refused",
			slog.String("reason", identitydom.ErrSessionExpired.Error()),
			slog.String("session_id", session.ID),
			slog.Time("expired_at", session.ExpiresAt))

		return Authenticated{}, ErrUnauthenticated
	}

	s.renew(ctx, session, now)

	return Authenticated{
		SessionID: session.ID,
		UserID:    session.UserID,
		Email:     row.Email,
		Name:      row.Name,
		IsOwner:   row.IsOwner,
	}, nil
}

// renew slides the deadline when it has moved far enough to be worth a write.
//
// A failed renewal is logged and the request continues, on purpose: the
// session is valid, the caller is who they say they are, and refusing would
// turn a slow database into a logout for everyone. What is lost is that the
// session may expire earlier than it would have.
func (s *Sessions) renew(ctx context.Context, session identitydom.Session, now time.Time) {
	if !session.NeedsRenewal(now) {
		return
	}

	if _, err := s.store.TouchSession(ctx, identityrepo.TouchSessionParams{
		ID:        session.ID,
		ExpiresAt: pgtype.Timestamptz{Time: session.RenewedExpiry(now), Valid: true},
	}); err != nil {
		s.log.WarnContext(ctx, "session deadline was not renewed",
			slog.String("session_id", session.ID),
			slog.String("error", err.Error()))
	}
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
