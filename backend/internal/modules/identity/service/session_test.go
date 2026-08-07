package service_test

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
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

	// The listing half, kept separate from rows because those are keyed by
	// token digest and this query never sees a token.
	listed         []identityrepo.ListSessionsByUserRow
	listedFor      []string
	revokedByID    []identityrepo.DeleteSessionForUserParams
	revokedOthers  []identityrepo.DeleteOtherSessionsForUserParams
	revokeRowCount int64
}

func (s *storeStub) ListSessionsByUser(_ context.Context, userID string) ([]identityrepo.ListSessionsByUserRow, error) {
	if s.failWith != nil {
		return nil, s.failWith
	}

	s.listedFor = append(s.listedFor, userID)

	return s.listed, nil
}

func (s *storeStub) DeleteSessionForUser(_ context.Context, arg identityrepo.DeleteSessionForUserParams) (int64, error) {
	if s.failWith != nil {
		return 0, s.failWith
	}

	s.revokedByID = append(s.revokedByID, arg)

	return s.revokeRowCount, nil
}

func (s *storeStub) DeleteOtherSessionsForUser(_ context.Context, arg identityrepo.DeleteOtherSessionsForUserParams) (int64, error) {
	if s.failWith != nil {
		return 0, s.failWith
	}

	s.revokedOthers = append(s.revokedOthers, arg)

	return s.revokeRowCount, nil
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

// listing builds a stub holding the given sessions.
func listing(rows ...identityrepo.ListSessionsByUserRow) *storeStub {
	return &storeStub{listed: rows}
}

func sessionRow(id, agent string) identityrepo.ListSessionsByUserRow {
	return identityrepo.ListSessionsByUserRow{
		ID:         id,
		UserAgent:  agent,
		CreatedAt:  pgtype.Timestamptz{Time: testNow.Add(-time.Hour), Valid: true},
		LastSeenAt: pgtype.Timestamptz{Time: testNow, Valid: true},
		ExpiresAt:  pgtype.Timestamptz{Time: testNow.Add(time.Hour), Valid: true},
	}
}

const (
	currentID = "0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
	otherID   = "0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5c"
)

// The listing has to say which row is the caller's own, or the screen cannot
// warn somebody before they sign themselves out.
func TestTheListingMarksTheSessionMakingTheRequest(t *testing.T) {
	t.Parallel()

	store := listing(sessionRow(currentID, "Firefox"), sessionRow(otherID, "Chrome"))

	got, err := newSessions(store).List(t.Context(), "user-1", currentID)
	if err != nil {
		t.Fatalf("List(): %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2", len(got))
	}

	if !got[0].Current {
		t.Error("the caller's own session is not marked current")
	}

	if got[1].Current {
		t.Error("another session was marked current")
	}
}

// The query must be given the caller's own id and nothing else. This is the
// read that must never be able to return somebody else's sessions.
func TestTheListingIsAskedForTheCallersOwnSessionsOnly(t *testing.T) {
	t.Parallel()

	store := listing(sessionRow(currentID, "Firefox"))

	if _, err := newSessions(store).List(t.Context(), "user-1", currentID); err != nil {
		t.Fatalf("List(): %v", err)
	}

	if len(store.listedFor) != 1 || store.listedFor[0] != "user-1" {
		t.Errorf("the listing was asked for %v, want exactly [user-1]", store.listedFor)
	}
}

// Nothing in the summary may carry the credential. A list endpoint is where one
// stray field becomes a token sitting in a JSON body.
func TestASummaryCarriesNoCredential(t *testing.T) {
	t.Parallel()

	store := listing(sessionRow(currentID, "Firefox"))

	got, err := newSessions(store).List(t.Context(), "user-1", currentID)
	if err != nil {
		t.Fatalf("List(): %v", err)
	}

	// Reflection rather than reading the struct, so that a field added later is
	// caught by this test instead of by whoever finds it in a response.
	summary := reflect.TypeOf(got[0])
	for i := range summary.NumField() {
		switch name := strings.ToLower(summary.Field(i).Name); {
		case strings.Contains(name, "token"), strings.Contains(name, "hash"), strings.Contains(name, "secret"):
			t.Errorf("SessionSummary has a field named %q", summary.Field(i).Name)
		}
	}
}

// Ownership is enforced by the statement, not by a check afterwards, so the
// user id has to reach the query.
func TestRevokingCarriesTheOwnerIntoTheStatement(t *testing.T) {
	t.Parallel()

	store := &storeStub{revokeRowCount: 1}

	if err := newSessions(store).RevokeByID(t.Context(), "user-1", otherID); err != nil {
		t.Fatalf("RevokeByID(): %v", err)
	}

	if len(store.revokedByID) != 1 {
		t.Fatalf("the delete ran %d times, want 1", len(store.revokedByID))
	}

	if got := store.revokedByID[0]; got.UserID != "user-1" || got.ID != otherID {
		t.Errorf("deleted %+v, want the caller's id and the named session", got)
	}
}

// Somebody else's session and a session that does not exist are one answer.
// Telling them apart would confirm which session ids are real.
func TestRevokingSomethingThatIsNotYoursIsNotFound(t *testing.T) {
	t.Parallel()

	// Zero rows deleted is what the WHERE clause produces for both cases.
	err := newSessions(&storeStub{revokeRowCount: 0}).RevokeByID(t.Context(), "user-1", otherID)

	if !errors.Is(err, identitysvc.ErrSessionNotFound) {
		t.Errorf("RevokeByID() = %v, want ErrSessionNotFound", err)
	}
}

// A malformed id must not reach the database. The column is uuid, so PostgreSQL
// would reject the statement and turn "no such session" into a 500 — and give
// the caller a way to tell a malformed id from a real one that is not theirs.
func TestAMalformedSessionIdIsNotFoundRatherThanAnError(t *testing.T) {
	t.Parallel()

	store := &storeStub{revokeRowCount: 1}

	for _, id := range []string{"", "not-a-uuid", "../../etc/passwd", "1; DROP TABLE sessions"} {
		err := newSessions(store).RevokeByID(t.Context(), "user-1", id)

		if !errors.Is(err, identitysvc.ErrSessionNotFound) {
			t.Errorf("RevokeByID(%q) = %v, want ErrSessionNotFound", id, err)
		}
	}

	if len(store.revokedByID) != 0 {
		t.Errorf("%d malformed ids reached the database", len(store.revokedByID))
	}
}

// Signing out everywhere else keeps the session making the request: an answer
// the caller cannot receive while still signed in is not what they asked for.
func TestRevokingTheOthersKeepsTheCurrentOne(t *testing.T) {
	t.Parallel()

	store := &storeStub{revokeRowCount: 3}

	deleted, err := newSessions(store).RevokeOthers(t.Context(), "user-1", currentID)
	if err != nil {
		t.Fatalf("RevokeOthers(): %v", err)
	}

	if deleted != 3 {
		t.Errorf("reported %d revoked, want 3", deleted)
	}

	if len(store.revokedOthers) != 1 {
		t.Fatalf("the delete ran %d times, want 1", len(store.revokedOthers))
	}

	if got := store.revokedOthers[0]; got.UserID != "user-1" || got.CurrentID != currentID {
		t.Errorf("deleted %+v, want the caller's id and their current session kept", got)
	}
}

// A current session id that is not a uuid cannot reach the database either: the
// statement excludes one row by id, so an id that matches nothing would delete
// every session the caller has, including the one they are using.
func TestAMalformedCurrentIdNeverBecomesADeleteEverything(t *testing.T) {
	t.Parallel()

	store := &storeStub{revokeRowCount: 9}

	if _, err := newSessions(store).RevokeOthers(t.Context(), "user-1", "not-a-uuid"); err == nil {
		t.Error("RevokeOthers() accepted a malformed current session id")
	}

	if len(store.revokedOthers) != 0 {
		t.Error("a malformed current session id reached the database")
	}
}

// A database that will not answer is reported, not turned into an empty list.
// An empty list reads as "nothing is signed in", which is the opposite of what
// somebody checking this screen needs to know.
func TestAFailedListingIsAnErrorRatherThanNoSessions(t *testing.T) {
	t.Parallel()

	store := &storeStub{failWith: errListingBroken}

	got, err := newSessions(store).List(t.Context(), "user-1", currentID)
	if err == nil {
		t.Fatal("List() returned no error while the database was failing")
	}

	if got != nil {
		t.Errorf("List() returned %d sessions alongside an error", len(got))
	}
}

var errListingBroken = errors.New("postgres is down")
