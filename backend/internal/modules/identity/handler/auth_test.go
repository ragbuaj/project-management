package handler_test

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
	identitysvc "github.com/ragbuaj/project-management/backend/internal/modules/identity/service"
)

const (
	testEmail    = "member@example.test"
	testPassword = "kuda benar baterai steples"
)

// store implements both halves of the repository the services need. The SQL
// has its own tests against a real database; what is exercised here is the
// HTTP shape.
type store struct {
	users    map[string]identityrepo.GetUserByEmailRow
	sessions map[string]identityrepo.GetSessionByTokenHashRow

	deleted    [][]byte
	failLogin  error
	failIssue  error
	failLogout error
}

func newStore(t *testing.T) *store {
	t.Helper()

	hash, err := identitydom.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword(): %v", err)
	}

	return &store{
		users: map[string]identityrepo.GetUserByEmailRow{
			testEmail: {
				ID:           "user-1",
				Email:        testEmail,
				Name:         "Member",
				PasswordHash: hash,
				Timezone:     "Asia/Jakarta",
				Role:         "owner",
			},
		},
		sessions: map[string]identityrepo.GetSessionByTokenHashRow{},
	}
}

func (s *store) GetUserByEmail(_ context.Context, email string) (identityrepo.GetUserByEmailRow, error) {
	if s.failLogin != nil {
		return identityrepo.GetUserByEmailRow{}, s.failLogin
	}

	row, ok := s.users[strings.ToLower(email)]
	if !ok {
		return identityrepo.GetUserByEmailRow{}, pgx.ErrNoRows
	}

	return row, nil
}

func (s *store) UpdateUserPasswordHash(context.Context, identityrepo.UpdateUserPasswordHashParams) (int64, error) {
	return 1, nil
}

func (s *store) CreateSession(_ context.Context, arg identityrepo.CreateSessionParams) (identityrepo.CreateSessionRow, error) {
	if s.failIssue != nil {
		return identityrepo.CreateSessionRow{}, s.failIssue
	}

	return identityrepo.CreateSessionRow{
		ID:         "session-1",
		UserID:     arg.UserID,
		CreatedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
		LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
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
	if s.failLogout != nil {
		return 0, s.failLogout
	}

	s.deleted = append(s.deleted, hash)

	return 1, nil
}

func newAuth(t *testing.T, s *store) *identityhttp.Auth {
	t.Helper()

	log := slog.New(slog.DiscardHandler)

	credentials, err := identitysvc.NewCredentials(s, log)
	if err != nil {
		t.Fatalf("NewCredentials(): %v", err)
	}

	return identityhttp.NewAuth(credentials, identitysvc.NewSessions(s, log, time.Now), log)
}

func postLogin(t *testing.T, auth *identityhttp.Auth, body string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	w := httptest.NewRecorder()

	auth.Login(w, r)

	return w
}

func sessionCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()

	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == identityhttp.SessionCookieName {
			return cookie
		}
	}

	t.Fatalf("no %s cookie in the response", identityhttp.SessionCookieName)

	return nil
}

func TestLoggingInReturnsTheUserAndSetsTheSessionCookie(t *testing.T) {
	t.Parallel()

	w := postLogin(t, newAuth(t, newStore(t)), `{"email":"member@example.test","password":"kuda benar baterai steples"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", w.Code, w.Body.String())
	}

	var body struct {
		User struct {
			ID       string `json:"id"`
			Email    string `json:"email"`
			Name     string `json:"name"`
			Timezone string `json:"timezone"`
			Role     string `json:"role"`
		} `json:"user"`
	}

	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not the shape the contract declares: %v", err)
	}

	if body.User.ID != "user-1" || body.User.Email != testEmail || body.User.Timezone != "Asia/Jakarta" || body.User.Role != "owner" {
		t.Errorf("user came back as %+v", body.User)
	}
}

// Every attribute here is load-bearing, and a browser silently ignores a
// __Host- cookie that is missing any of them.
func TestTheSessionCookieCarriesTheAttributesADR0005Requires(t *testing.T) {
	t.Parallel()

	w := postLogin(t, newAuth(t, newStore(t)), `{"email":"member@example.test","password":"kuda benar baterai steples"}`)
	cookie := sessionCookie(t, w)

	if cookie.Value == "" {
		t.Error("the cookie carries no token")
	}

	if !cookie.HttpOnly {
		t.Error("the cookie is readable by script, so an XSS is an account takeover")
	}

	if !cookie.Secure {
		t.Error("the cookie is not Secure, which a browser refuses for a __Host- name anyway")
	}

	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite is %v, want Lax — ADR-0005 rules out Strict because it breaks card links from chat", cookie.SameSite)
	}

	if cookie.Path != "/" {
		t.Errorf("Path is %q, want / — required by the __Host- prefix", cookie.Path)
	}

	if cookie.Domain != "" {
		t.Errorf("Domain is %q, but the __Host- prefix forbids naming one", cookie.Domain)
	}

	if cookie.Expires.Before(time.Now().Add(identitydom.SessionIdleWindow - time.Hour)) {
		t.Errorf("the cookie expires at %v, well before the session does", cookie.Expires)
	}
}

// The contract declares one response for an unregistered address and a wrong
// password. That includes the wording: a different message is a different
// answer.
func TestAWrongPasswordAndAnUnknownAddressAnswerIdentically(t *testing.T) {
	t.Parallel()

	auth := newAuth(t, newStore(t))

	wrong := postLogin(t, auth, `{"email":"member@example.test","password":"kuda salah baterai steples"}`)
	unknown := postLogin(t, auth, `{"email":"nobody@example.test","password":"kuda benar baterai steples"}`)

	if wrong.Code != http.StatusUnauthorized || unknown.Code != http.StatusUnauthorized {
		t.Fatalf("statuses were %d and %d, want 401 for both", wrong.Code, unknown.Code)
	}

	if wrong.Body.String() != unknown.Body.String() {
		t.Errorf("the two refusals differ:\n  wrong password: %s\n  unknown address: %s", wrong.Body, unknown.Body)
	}

	for _, w := range []*httptest.ResponseRecorder{wrong, unknown} {
		if len(w.Result().Cookies()) != 0 {
			t.Error("a refused login still set a cookie")
		}
	}
}

func TestARequestThatCannotBeReadIsRefusedBeforeAnythingIsHashed(t *testing.T) {
	t.Parallel()

	auth := newAuth(t, newStore(t))

	cases := []struct {
		name string
		body string
	}{
		{"not JSON at all", "email=member@example.test"},
		{"truncated JSON", `{"email":"member@example.test"`},
		{"no fields", `{}`},
		{"an empty password", `{"email":"member@example.test","password":""}`},
		{"an empty address", `{"email":"","password":"kuda benar baterai steples"}`},
		{"a body larger than the limit", `{"email":"` + strings.Repeat("a", 8<<10) + `"}`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			w := postLogin(t, auth, c.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %s", w.Code, w.Body.String())
			}

			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}

			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("error response is not the declared shape: %v", err)
			}

			if body.Error.Code != "VALIDATION_FAILED" {
				t.Errorf("error code is %q, want VALIDATION_FAILED", body.Error.Code)
			}
		})
	}
}

// A session that cannot be written is not a login. Answering 200 would hand
// back a cookie that authenticates nothing.
func TestALoginWhoseSessionCannotBeWrittenIsNotASuccess(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	s.failIssue = pgx.ErrTxClosed

	w := postLogin(t, newAuth(t, s), `{"email":"member@example.test","password":"kuda benar baterai steples"}`)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", w.Code)
	}

	if len(w.Result().Cookies()) != 0 {
		t.Error("a failed login still set a cookie")
	}
}

func TestLoggingOutRevokesTheSessionAndClearsTheCookie(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	auth := newAuth(t, s)

	login := postLogin(t, auth, `{"email":"member@example.test","password":"kuda benar baterai steples"}`)
	token := sessionCookie(t, login).Value

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/auth/logout", nil)
	r.AddCookie(&http.Cookie{Name: identityhttp.SessionCookieName, Value: token})

	w := httptest.NewRecorder()
	auth.Logout(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204", w.Code)
	}

	if len(s.deleted) != 1 {
		t.Errorf("logout deleted %d sessions, want 1", len(s.deleted))
	}

	cleared := sessionCookie(t, w)
	if cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Errorf("the cookie was not cleared: value %q, max-age %d", cleared.Value, cleared.MaxAge)
	}
}

// Logging out without a session is what a stale tab does. It must end in the
// same place as a real logout rather than in an error.
func TestLoggingOutWithoutASessionStillSucceeds(t *testing.T) {
	t.Parallel()

	s := newStore(t)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/auth/logout", nil)
	w := httptest.NewRecorder()

	newAuth(t, s).Logout(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204", w.Code)
	}

	if len(s.deleted) != 0 {
		t.Error("a logout with no cookie still reached the database")
	}

	// The cookie is cleared even when there was nothing to revoke, so a stale
	// one stops being sent on the next request.
	if cleared := sessionCookie(t, w); cleared.Value != "" {
		t.Errorf("the cookie was not cleared: %q", cleared.Value)
	}
}

// The one answer logout must never give: saying the session is gone when it is
// still live.
func TestALogoutThatCannotDeleteTheSessionSaysSo(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	auth := newAuth(t, s)

	login := postLogin(t, auth, `{"email":"member@example.test","password":"kuda benar baterai steples"}`)
	token := sessionCookie(t, login).Value

	s.failLogout = pgx.ErrTxClosed

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/auth/logout", nil)
	r.AddCookie(&http.Cookie{Name: identityhttp.SessionCookieName, Value: token})

	w := httptest.NewRecorder()
	auth.Logout(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status %d, want 500 — the session is still there", w.Code)
	}
}
