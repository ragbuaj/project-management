package route_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	identitydom "github.com/ragbuaj/project-management/backend/internal/modules/identity/domain"
	identityhttp "github.com/ragbuaj/project-management/backend/internal/modules/identity/handler"
	identityrepo "github.com/ragbuaj/project-management/backend/internal/modules/identity/repository"
	identityroute "github.com/ragbuaj/project-management/backend/internal/modules/identity/route"
	identitysvc "github.com/ragbuaj/project-management/backend/internal/modules/identity/service"
)

const (
	testEmail    = "member@example.test"
	testPassword = "kuda benar baterai steples"
)

// store keeps the sessions it issues, so a token from a real login can be
// presented to a later request. That round trip is the whole point of these
// tests: the pieces are already covered on their own.
type store struct {
	user     identityrepo.GetUserByEmailRow
	sessions map[string]identityrepo.GetSessionByTokenHashRow
}

func newStore(t *testing.T) *store {
	t.Helper()

	hash, err := identitydom.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword(): %v", err)
	}

	return &store{
		user: identityrepo.GetUserByEmailRow{
			ID:           "user-1",
			Email:        testEmail,
			Name:         "Member",
			PasswordHash: hash,
			Timezone:     "Asia/Jakarta",
			Role:         "owner",
		},
		sessions: map[string]identityrepo.GetSessionByTokenHashRow{},
	}
}

func (s *store) GetUserByEmail(_ context.Context, email string) (identityrepo.GetUserByEmailRow, error) {
	if !strings.EqualFold(email, s.user.Email) {
		return identityrepo.GetUserByEmailRow{}, pgx.ErrNoRows
	}

	return s.user, nil
}

func (s *store) UpdateUserPasswordHash(context.Context, identityrepo.UpdateUserPasswordHashParams) (int64, error) {
	return 1, nil
}

func (s *store) CreateSession(_ context.Context, arg identityrepo.CreateSessionParams) (identityrepo.CreateSessionRow, error) {
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}

	s.sessions[string(arg.TokenHash)] = identityrepo.GetSessionByTokenHashRow{
		ID:         "session-1",
		UserID:     arg.UserID,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  arg.ExpiresAt,
		Email:      s.user.Email,
		Name:       s.user.Name,
		Timezone:   s.user.Timezone,
		Role:       s.user.Role,
	}

	return identityrepo.CreateSessionRow{
		ID:         "session-1",
		UserID:     arg.UserID,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  arg.ExpiresAt,
	}, nil
}

func (s *store) GetSessionByTokenHash(_ context.Context, hash []byte) (identityrepo.GetSessionByTokenHashRow, error) {
	row, ok := s.sessions[string(hash)]
	if !ok {
		return identityrepo.GetSessionByTokenHashRow{}, pgx.ErrNoRows
	}

	return row, nil
}

func (s *store) TouchSession(context.Context, identityrepo.TouchSessionParams) (int64, error) {
	return 1, nil
}

func (s *store) DeleteSessionByTokenHash(_ context.Context, hash []byte) (int64, error) {
	if _, ok := s.sessions[string(hash)]; !ok {
		return 0, nil
	}

	delete(s.sessions, string(hash))

	return 1, nil
}

// fullStore is both halves the services need, which every stub here happens
// to be. Naming it avoids a type assertion in the helper below.
type fullStore interface {
	identitysvc.SessionStore
	identitysvc.UserStore
}

func newMux(t *testing.T, s fullStore) *http.ServeMux {
	t.Helper()

	log := slog.New(slog.DiscardHandler)

	credentials, err := identitysvc.NewCredentials(s, log)
	if err != nil {
		t.Fatalf("NewCredentials(): %v", err)
	}

	sessions := identitysvc.NewSessions(s, log, time.Now)

	// A guard with no counters refuses nothing, so these tests keep asking
	// about routing and sessions rather than about rate limits. What the guard
	// does is settled in the handler and service tests.
	guard := identitysvc.NewLoginGuard(openCounter{}, openCounter{}, openCounter{}, log)

	mux := http.NewServeMux()
	identityroute.Register(mux,
		identityhttp.NewAuth(credentials, sessions, guard, nil, log), sessions, log)

	return mux
}

// openCounter never refuses and never remembers anything.
type openCounter struct{}

func (openCounter) Check(context.Context, string) (bool, time.Duration, error) {
	return true, 0, nil
}

func (openCounter) Record(context.Context, string) error { return nil }
func (openCounter) Reset(context.Context, string) error  { return nil }

// signIn logs in through the mux and returns the session cookie.
func signIn(t *testing.T, mux *http.ServeMux) *http.Cookie {
	t.Helper()

	body := `{"email":"` + testEmail + `","password":"` + testPassword + `"}`

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("login answered %d: %s", w.Code, w.Body)
	}

	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == identityhttp.SessionCookieName {
			return cookie
		}
	}

	t.Fatal("login set no session cookie")

	return nil
}

func get(t *testing.T, mux *http.ServeMux, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	return w
}

// The round trip the pieces cannot prove on their own: a cookie set by login
// is accepted by /me, and the identity it returns is read back through the
// session rather than remembered from the login.
func TestASessionFromLoginIsAcceptedByMe(t *testing.T) {
	t.Parallel()

	mux := newMux(t, newStore(t))

	w := get(t, mux, "/api/v1/me", signIn(t, mux))
	if w.Code != http.StatusOK {
		t.Fatalf("/me answered %d: %s", w.Code, w.Body)
	}

	var body struct {
		ID       string `json:"id"`
		Email    string `json:"email"`
		Name     string `json:"name"`
		Timezone string `json:"timezone"`
		Role     string `json:"role"`
	}

	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("/me is not the shape the contract declares: %v", err)
	}

	if body.ID != "user-1" || body.Email != testEmail || body.Timezone != "Asia/Jakarta" || body.Role != "owner" {
		t.Errorf("/me returned %+v", body)
	}
}

// Every way of arriving without a live session is one answer.
func TestMeRefusesEveryRequestWithoutALiveSession(t *testing.T) {
	t.Parallel()

	mux := newMux(t, newStore(t))

	unissued, _, err := identitydom.NewSessionToken()
	if err != nil {
		t.Fatalf("NewSessionToken(): %v", err)
	}

	cases := []struct {
		name    string
		cookies []*http.Cookie
	}{
		{"no cookie at all", nil},
		{"a cookie that was never a token", []*http.Cookie{{Name: identityhttp.SessionCookieName, Value: "not-a-token"}}},
		{"a token with no session behind it", []*http.Cookie{{Name: identityhttp.SessionCookieName, Value: unissued}}},
		{"the wrong cookie name", []*http.Cookie{{Name: "session", Value: unissued}}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			w := get(t, mux, "/api/v1/me", c.cookies...)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401: %s", w.Code, w.Body)
			}

			var body struct {
				Error struct {
					Code      string `json:"code"`
					RequestID string `json:"request_id"`
				} `json:"error"`
			}

			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("the refusal is not the declared error shape: %v", err)
			}

			if body.Error.Code != "UNAUTHENTICATED" {
				t.Errorf("error code is %q, want UNAUTHENTICATED", body.Error.Code)
			}
		})
	}
}

// Logout has to be effective on the very next request, which is the property
// ADR-0005 chose opaque sessions for.
func TestASessionStopsWorkingTheMomentItIsRevoked(t *testing.T) {
	t.Parallel()

	mux := newMux(t, newStore(t))
	cookie := signIn(t, mux)

	if w := get(t, mux, "/api/v1/me", cookie); w.Code != http.StatusOK {
		t.Fatalf("/me answered %d before logout", w.Code)
	}

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/auth/logout", nil)
	r.AddCookie(cookie)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("logout answered %d", w.Code)
	}

	if after := get(t, mux, "/api/v1/me", cookie); after.Code != http.StatusUnauthorized {
		t.Errorf("/me answered %d with the revoked cookie, want 401", after.Code)
	}
}

// The methods and paths are the contract's. A handler mounted at the wrong
// verb is a 405 nobody notices until the client is written.
func TestTheRoutesAreMountedWhereTheContractSaysTheyAre(t *testing.T) {
	t.Parallel()

	mux := newMux(t, newStore(t))

	cases := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, "/api/v1/auth/login", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/v1/auth/logout", http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/v1/me", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/v1/auth/me", http.StatusNotFound},
		{http.MethodGet, "/me", http.StatusNotFound},
	}

	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), c.method, c.path, nil)
			w := httptest.NewRecorder()

			mux.ServeHTTP(w, r)

			if w.Code != c.want {
				t.Errorf("status %d, want %d", w.Code, c.want)
			}
		})
	}
}

// brokenSessions fails the lookup with something other than "no rows".
type brokenSessions struct {
	*store

	err error
}

func (b *brokenSessions) GetSessionByTokenHash(context.Context, []byte) (identityrepo.GetSessionByTokenHashRow, error) {
	return identityrepo.GetSessionByTokenHashRow{}, b.err
}

// A database that will not answer is not an unauthenticated caller. Answering
// 401 sends people to the login page, where signing in fails too, and the
// outage is reported as "my password stopped working".
func TestADatabaseFailureIsNotAnsweredAsBeingSignedOut(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	mux := newMux(t, s)
	cookie := signIn(t, mux)

	broken := newMux(t, &brokenSessions{store: s, err: pgx.ErrTxClosed})

	w := get(t, broken, "/api/v1/me", cookie)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500: %s", w.Code, w.Body)
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}

	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("the response is not the declared error shape: %v", err)
	}

	if body.Error.Code != "INTERNAL" {
		t.Errorf("error code is %q, want INTERNAL", body.Error.Code)
	}
}
