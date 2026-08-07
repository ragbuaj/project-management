package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	identitydom "github.com/ragbuaj/project-management/backend/internal/modules/identity/domain"
	identityrepo "github.com/ragbuaj/project-management/backend/internal/modules/identity/repository"
	identitysvc "github.com/ragbuaj/project-management/backend/internal/modules/identity/service"
)

// failingStore fails the lookup with something other than "no rows". storeStub
// cannot express that: a missing row there is pgx.ErrNoRows by construction,
// which is exactly the case this must not be confused with.
type failingStore struct {
	storeStub

	err error
}

func (s *failingStore) GetSessionByTokenHash(context.Context, []byte) (identityrepo.GetSessionByTokenHashRow, error) {
	return identityrepo.GetSessionByTokenHashRow{}, s.err
}

func capturingLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func sessionsLogging(store identitysvc.SessionStore, buf *bytes.Buffer) *identitysvc.Sessions {
	return identitysvc.NewSessions(store, capturingLogger(buf), func() time.Time { return testNow })
}

func TestAValidTokenResolvesToItsCaller(t *testing.T) {
	t.Parallel()

	store := &storeStub{}
	token := store.seed(t, testNow.Add(-time.Hour), testNow.Add(identitydom.SessionIdleWindow))

	who, err := newSessions(store).Authenticate(t.Context(), token)
	if err != nil {
		t.Fatalf("Authenticate(): %v", err)
	}

	want := identitysvc.Authenticated{
		SessionID: "session-1",
		UserID:    "user-1",
		Email:     "member@example.test",
		Name:      "Member",
	}

	if who != want {
		t.Errorf("Authenticate() = %+v, want %+v", who, want)
	}
}

// Every way of not being signed in has to arrive as one error. A caller that
// can tell "no such session" from "expired" learns whether a stolen token was
// ever real.
func TestEveryWayOfNotBeingSignedInLooksTheSame(t *testing.T) {
	t.Parallel()

	store := &storeStub{}
	expired := store.seed(t, testNow.Add(-30*24*time.Hour), testNow.Add(-time.Second))

	unissued, _, err := identitydom.NewSessionToken()
	if err != nil {
		t.Fatalf("NewSessionToken(): %v", err)
	}

	sessions := identitysvc.NewSessions(store, slog.New(slog.DiscardHandler), func() time.Time { return testNow })

	cases := []struct {
		name  string
		token string
	}{
		{"no cookie at all", ""},
		{"a cookie that was never a token", "not-a-token"},
		{"a token with no session behind it", unissued},
		{"a session that has expired", expired},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			who, err := sessions.Authenticate(t.Context(), c.token)
			if !errors.Is(err, identitysvc.ErrUnauthenticated) {
				t.Fatalf("Authenticate() = %+v, %v; want ErrUnauthenticated", who, err)
			}

			if who != (identitysvc.Authenticated{}) {
				t.Errorf("Authenticate() returned %+v alongside the refusal", who)
			}
		})
	}
}

// The log is allowed to know what the answer is not. An expired session is
// worth a line; a malformed cookie is what every crawler produces and would be
// a line per request.
func TestTheLogSeparatesWhatTheAnswerDoesNot(t *testing.T) {
	t.Parallel()

	store := &storeStub{}
	expired := store.seed(t, testNow.Add(-30*24*time.Hour), testNow.Add(-time.Second))

	var buf bytes.Buffer
	sessions := sessionsLogging(store, &buf)

	if _, err := sessions.Authenticate(t.Context(), expired); !errors.Is(err, identitysvc.ErrUnauthenticated) {
		t.Fatalf("Authenticate() = %v, want ErrUnauthenticated", err)
	}

	var line struct {
		Reason    string `json:"reason"`
		SessionID string `json:"session_id"`
	}

	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &line); err != nil {
		t.Fatalf("an expired session produced no readable log line: %v", err)
	}

	if line.SessionID != "session-1" || !strings.Contains(line.Reason, "expired") {
		t.Errorf("log line was %+v, want it to name the expired session", line)
	}

	buf.Reset()

	if _, err := sessions.Authenticate(t.Context(), "not-a-token"); !errors.Is(err, identitysvc.ErrUnauthenticated) {
		t.Fatalf("Authenticate() = %v, want ErrUnauthenticated", err)
	}

	if buf.Len() != 0 {
		t.Errorf("a malformed cookie wrote %q to the log", buf.String())
	}
}

// Sliding on every request would be one UPDATE per request for the whole
// application — the cost of a login page nobody reloads, paid on every drag.
func TestTheDeadlineIsOnlyWrittenWhenItHasMovedEnough(t *testing.T) {
	t.Parallel()

	created := testNow.Add(-2 * time.Hour)

	cases := []struct {
		name    string
		expires time.Time
		touched bool
	}{
		{"a session used minutes ago", testNow.Add(identitydom.SessionIdleWindow - time.Minute), false},
		{"a session last used yesterday", testNow.Add(identitydom.SessionIdleWindow - 24*time.Hour), true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			store := &storeStub{}
			token := store.seed(t, created, c.expires)

			if _, err := newSessions(store).Authenticate(t.Context(), token); err != nil {
				t.Fatalf("Authenticate(): %v", err)
			}

			if touched := len(store.touched) == 1; touched != c.touched {
				t.Fatalf("the session was written %d times, want touched=%v", len(store.touched), c.touched)
			}

			if !c.touched {
				return
			}

			want := identitydom.Session{CreatedAt: created, ExpiresAt: c.expires}.RenewedExpiry(testNow)
			if got := store.touched[0].ExpiresAt.Time; !got.Equal(want) {
				t.Errorf("renewed to %v, want %v", got, want)
			}
		})
	}
}

// A session near its ninety-day ceiling must not be pushed past it by being
// used. The deadline the service writes is the one the domain computed.
func TestRenewalNeverPushesASessionPastItsCeiling(t *testing.T) {
	t.Parallel()

	created := testNow.Add(-identitydom.SessionAbsoluteWindow + 24*time.Hour)

	store := &storeStub{}
	token := store.seed(t, created, testNow.Add(time.Hour))

	if _, err := newSessions(store).Authenticate(t.Context(), token); err != nil {
		t.Fatalf("Authenticate(): %v", err)
	}

	if len(store.touched) != 1 {
		t.Fatalf("the session was written %d times, want 1", len(store.touched))
	}

	ceiling := created.Add(identitydom.SessionAbsoluteWindow)
	if got := store.touched[0].ExpiresAt.Time; !got.Equal(ceiling) {
		t.Errorf("renewed to %v, want the ceiling at %v", got, ceiling)
	}
}

// A database that cannot take the renewal must not turn into a logout for
// everyone. The session is valid; the write is an optimisation.
func TestAFailedRenewalDoesNotRefuseTheRequest(t *testing.T) {
	t.Parallel()

	store := &storeStub{}
	token := store.seed(t, testNow.Add(-2*time.Hour), testNow.Add(identitydom.SessionIdleWindow-24*time.Hour))
	store.failWith = errors.New("connection reset")

	var buf bytes.Buffer

	who, err := sessionsLogging(store, &buf).Authenticate(t.Context(), token)
	if err != nil {
		t.Fatalf("Authenticate() = %v, want the request to succeed", err)
	}

	if who.UserID != "user-1" {
		t.Errorf("Authenticate() returned %+v, want the seeded caller", who)
	}

	if !strings.Contains(buf.String(), "not renewed") {
		t.Errorf("a failed renewal was swallowed without a word: %q", buf.String())
	}
}

// A store that fails for any other reason is not an unauthenticated caller.
// Answering 401 to a broken database tells people to sign in again, which they
// cannot, and hides the outage behind a login page.
func TestAStoreFailureIsNotReportedAsBeingSignedOut(t *testing.T) {
	t.Parallel()

	store := &failingStore{err: errors.New("connection reset")}

	_, err := newSessions(store).Authenticate(t.Context(), strings.Repeat("A", 43))
	if err == nil {
		t.Fatal("Authenticate() succeeded against a failing store")
	}

	if errors.Is(err, identitysvc.ErrUnauthenticated) {
		t.Errorf("a store failure was reported as ErrUnauthenticated: %v", err)
	}
}
