package service_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	identitydom "github.com/ragbuaj/project-management/backend/internal/modules/identity/domain"
	identityrepo "github.com/ragbuaj/project-management/backend/internal/modules/identity/repository"
	identitysvc "github.com/ragbuaj/project-management/backend/internal/modules/identity/service"
)

// storeStub stands in for the repository. It is a hand-written fake rather
// than a mocked SQL driver: what is tested here is what the service decides,
// and the SQL has its own tests against a real database.
type storeStub struct {
	rows map[string]identityrepo.GetSessionByTokenHashRow

	created  []identityrepo.CreateSessionParams
	touched  []identityrepo.TouchSessionParams
	deleted  [][]byte
	failWith error
}

func (s *storeStub) GetSessionByTokenHash(_ context.Context, hash []byte) (identityrepo.GetSessionByTokenHashRow, error) {
	row, ok := s.rows[string(hash)]
	if !ok {
		return identityrepo.GetSessionByTokenHashRow{}, pgx.ErrNoRows
	}

	return row, nil
}

func (s *storeStub) TouchSession(_ context.Context, arg identityrepo.TouchSessionParams) (int64, error) {
	if s.failWith != nil {
		return 0, s.failWith
	}

	s.touched = append(s.touched, arg)

	return 1, nil
}

// seed puts a live session in the stub and returns the token that reaches it.
func (s *storeStub) seed(t *testing.T, created, expires time.Time) string {
	t.Helper()

	token, digest, err := identitydom.NewSessionToken()
	if err != nil {
		t.Fatalf("NewSessionToken(): %v", err)
	}

	if s.rows == nil {
		s.rows = map[string]identityrepo.GetSessionByTokenHashRow{}
	}

	s.rows[string(digest)] = identityrepo.GetSessionByTokenHashRow{
		ID:         "session-1",
		UserID:     "user-1",
		CreatedAt:  pgtype.Timestamptz{Time: created, Valid: true},
		LastSeenAt: pgtype.Timestamptz{Time: created, Valid: true},
		ExpiresAt:  pgtype.Timestamptz{Time: expires, Valid: true},
		Email:      "member@example.test",
		Name:       "Member",
	}

	return token
}

func (s *storeStub) CreateSession(_ context.Context, arg identityrepo.CreateSessionParams) (identityrepo.CreateSessionRow, error) {
	if s.failWith != nil {
		return identityrepo.CreateSessionRow{}, s.failWith
	}

	s.created = append(s.created, arg)

	return identityrepo.CreateSessionRow{
		ID:         "session-1",
		UserID:     arg.UserID,
		CreatedAt:  pgtype.Timestamptz{Time: testNow, Valid: true},
		LastSeenAt: pgtype.Timestamptz{Time: testNow, Valid: true},
		ExpiresAt:  arg.ExpiresAt,
	}, nil
}

func (s *storeStub) DeleteSessionByTokenHash(_ context.Context, hash []byte) (int64, error) {
	if s.failWith != nil {
		return 0, s.failWith
	}

	s.deleted = append(s.deleted, hash)

	return 1, nil
}

func newSessions(store identitysvc.SessionStore) *identitysvc.Sessions {
	return identitysvc.NewSessions(store, slog.New(slog.DiscardHandler), func() time.Time { return testNow })
}

var testNow = time.Date(2026, 8, 7, 9, 0, 0, 0, time.UTC)

func TestIssuingASessionStoresTheDigestAndReturnsTheToken(t *testing.T) {
	t.Parallel()

	store := &storeStub{}
	sessions := newSessions(store)

	token, session, err := sessions.Issue(t.Context(), "user-1", "Mozilla/5.0")
	if err != nil {
		t.Fatalf("Issue(): %v", err)
	}

	if token == "" {
		t.Fatal("Issue() returned an empty token")
	}

	if session.UserID != "user-1" || session.ID != "session-1" {
		t.Errorf("Issue() returned %+v, want the row the store wrote", session)
	}

	if len(store.created) != 1 {
		t.Fatalf("Issue() wrote %d rows, want 1", len(store.created))
	}

	// What is stored must be the digest of the token, not the token. This is
	// the whole reason a database dump is not a pile of usable sessions.
	stored := store.created[0].TokenHash

	if strings.Contains(token, string(stored)) {
		t.Error("the stored value can be read out of the token")
	}

	digest, err := identitydom.SessionTokenDigest(token)
	if err != nil {
		t.Fatalf("the issued token does not survive SessionTokenDigest: %v", err)
	}

	if string(digest) != string(stored) {
		t.Error("the token does not hash to the digest that was stored, so it would never look up")
	}
}

func TestAnIssuedSessionCarriesTheIdleDeadline(t *testing.T) {
	t.Parallel()

	store := &storeStub{}

	if _, _, err := newSessions(store).Issue(t.Context(), "user-1", ""); err != nil {
		t.Fatalf("Issue(): %v", err)
	}

	want := identitydom.NewSessionExpiry(testNow)
	if got := store.created[0].ExpiresAt.Time; !got.Equal(want) {
		t.Errorf("session expires at %v, want %v", got, want)
	}
}

// A store that cannot write must not produce a token: the caller would hold a
// cookie that authenticates nothing, and would be told it had signed in.
func TestAFailedWriteYieldsNoToken(t *testing.T) {
	t.Parallel()

	store := &storeStub{failWith: errors.New("connection reset")}

	token, _, err := newSessions(store).Issue(t.Context(), "user-1", "")
	if err == nil {
		t.Fatal("Issue() reported success while the store was failing")
	}

	if token != "" {
		t.Errorf("Issue() returned the token %q alongside an error", token)
	}
}

// Logging out twice is a double-clicked button. Neither attempt is an error,
// and the caller ends up signed out either way.
func TestRevokingIsIdempotentAndForgiving(t *testing.T) {
	t.Parallel()

	store := &storeStub{}
	sessions := newSessions(store)

	token, _, err := sessions.Issue(t.Context(), "user-1", "")
	if err != nil {
		t.Fatalf("Issue(): %v", err)
	}

	if err := sessions.Revoke(t.Context(), token); err != nil {
		t.Fatalf("Revoke(): %v", err)
	}

	if len(store.deleted) != 1 {
		t.Fatalf("Revoke() deleted %d sessions, want 1", len(store.deleted))
	}

	// A cookie that was never a token must reach no query at all.
	if err := sessions.Revoke(t.Context(), "not-a-token"); err != nil {
		t.Errorf("Revoke() with a malformed cookie = %v, want no error", err)
	}

	if len(store.deleted) != 1 {
		t.Error("a malformed cookie still reached the database")
	}
}

// A store that cannot delete is a different matter: the session is still
// there, and telling the caller they are signed out when they are not is the
// one answer logout must never give.
func TestAFailedRevokeIsReported(t *testing.T) {
	t.Parallel()

	store := &storeStub{}
	sessions := newSessions(store)

	token, _, err := sessions.Issue(t.Context(), "user-1", "")
	if err != nil {
		t.Fatalf("Issue(): %v", err)
	}

	store.failWith = errors.New("connection reset")

	if err := sessions.Revoke(t.Context(), token); err == nil {
		t.Error("Revoke() reported success while the session was still there")
	}
}

// PostgreSQL rejects invalid UTF-8 in a text column outright. Without this a
// client could send a stray byte and make its own login fail — or discover it
// can make any login fail.
func TestAHostileUserAgentCannotBreakTheInsert(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		agent string
	}{
		{"a stray byte", "Mozilla/5.0 \xff\xfe"},
		{"nothing but invalid bytes", "\xff\xff\xff"},
		{"longer than the column should carry", strings.Repeat("A", 4096)},
		{"a multi-byte rune across the cut", strings.Repeat("A", 511) + "日本語"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			store := &storeStub{}

			if _, _, err := newSessions(store).Issue(t.Context(), "user-1", c.agent); err != nil {
				t.Fatalf("Issue(): %v", err)
			}

			stored := store.created[0].UserAgent

			if !utf8.ValidString(stored) {
				t.Errorf("stored user agent %q is not valid UTF-8", stored)
			}

			if len(stored) > 512 {
				t.Errorf("stored user agent is %d bytes, want at most 512", len(stored))
			}
		})
	}
}
