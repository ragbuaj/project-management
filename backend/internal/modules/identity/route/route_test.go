package route_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
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
	// Keyed by session id rather than digest: the listing query never sees a
	// token, which is the point of it.
	listable map[string]identityrepo.ListSessionsByUserRow
	issued   int
}

// newSessionID hands out uuid-shaped ids because the column is uuid and the
// service refuses anything else before it reaches the database. The fixture
// used to hand out "session-1", which no real row could ever be.
func (s *store) newSessionID() string {
	s.issued++

	return fmt.Sprintf("0199a1b2-c3d4-7e5f-8a9b-00000000%04d", s.issued)
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
		listable: map[string]identityrepo.ListSessionsByUserRow{},
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
	id := s.newSessionID()

	s.listable[id] = identityrepo.ListSessionsByUserRow{
		ID:         id,
		UserAgent:  arg.UserAgent,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  arg.ExpiresAt,
	}

	s.sessions[string(arg.TokenHash)] = identityrepo.GetSessionByTokenHashRow{
		ID:         id,
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
		ID:         id,
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

// The session-listing half. It is functional rather than a stub because these
// routes are the ones under test here; what the SQL enforces has its own tests
// against a real database.
func (s *store) ListSessionsByUser(_ context.Context, userID string) ([]identityrepo.ListSessionsByUserRow, error) {
	out := []identityrepo.ListSessionsByUserRow{}

	for _, row := range s.sessions {
		if row.UserID != userID {
			continue
		}

		if listed, ok := s.listable[row.ID]; ok {
			out = append(out, listed)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	return out, nil
}

func (s *store) DeleteSessionForUser(_ context.Context, arg identityrepo.DeleteSessionForUserParams) (int64, error) {
	for digest, row := range s.sessions {
		if row.ID == arg.ID && row.UserID == arg.UserID {
			delete(s.sessions, digest)
			delete(s.listable, row.ID)

			return 1, nil
		}
	}

	return 0, nil
}

func (s *store) DeleteOtherSessionsForUser(_ context.Context, arg identityrepo.DeleteOtherSessionsForUserParams) (int64, error) {
	var deleted int64

	for digest, row := range s.sessions {
		if row.UserID != arg.UserID || row.ID == arg.CurrentID {
			continue
		}

		delete(s.sessions, digest)
		delete(s.listable, row.ID)

		deleted++
	}

	return deleted, nil
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
		// The collection takes DELETE but not POST, and the single-session
		// route is the reverse of the collection's GET. Both patterns are
		// mounted, so ServeMux answers 405 rather than 404 — which is also how
		// this test tells "mounted" from "typo in the path".
		{http.MethodPost, "/api/v1/me/sessions", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/v1/me/sessions/0199a1b2-c3d4-7e5f-8a9b-000000000001", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/v1/me/session", http.StatusNotFound},
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

// del sends a DELETE through the mux with the given cookies.
func del(t *testing.T, mux *http.ServeMux, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, path, nil)
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	return w
}

type sessionsBody struct {
	Sessions []struct {
		ID        string `json:"id"`
		UserAgent string `json:"user_agent"`
		Current   bool   `json:"current"`
	} `json:"sessions"`
}

func listSessions(t *testing.T, mux *http.ServeMux, cookie *http.Cookie) sessionsBody {
	t.Helper()

	w := get(t, mux, "/api/v1/me/sessions", cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("listing answered %d: %s", w.Code, w.Body)
	}

	var body sessionsBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not the shape the contract declares: %v", err)
	}

	return body
}

// Signing in twice makes two sessions, and the listing has to show both and
// mark the one asking.
func TestTheListingShowsEverySessionAndMarksTheCurrentOne(t *testing.T) {
	t.Parallel()

	mux := newMux(t, newStore(t))

	first := signIn(t, mux)
	second := signIn(t, mux)

	body := listSessions(t, mux, second)

	if len(body.Sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(body.Sessions))
	}

	var current int

	for _, s := range body.Sessions {
		if s.Current {
			current++
		}
	}

	if current != 1 {
		t.Errorf("%d sessions are marked current, want exactly 1", current)
	}

	// And the other cookie sees the same two sessions with the mark moved.
	if got := listSessions(t, mux, first); len(got.Sessions) != 2 {
		t.Errorf("the first session sees %d sessions, want 2", len(got.Sessions))
	}
}

// No response from these endpoints may carry a token. This is the round trip
// the service test cannot make: it checks the JSON that actually goes out.
func TestNoSessionResponseCarriesATokenValue(t *testing.T) {
	t.Parallel()

	mux := newMux(t, newStore(t))
	cookie := signIn(t, mux)

	w := get(t, mux, "/api/v1/me/sessions", cookie)

	if strings.Contains(w.Body.String(), cookie.Value) {
		t.Error("the listing response contains the session token")
	}

	for _, forbidden := range []string{"token", "hash", "digest", "secret"} {
		if strings.Contains(strings.ToLower(w.Body.String()), forbidden) {
			t.Errorf("the listing response mentions %q", forbidden)
		}
	}
}

func TestRevokingOneSessionEndsItAndLeavesTheRest(t *testing.T) {
	t.Parallel()

	mux := newMux(t, newStore(t))

	doomed := signIn(t, mux)
	keeper := signIn(t, mux)

	var doomedID string

	for _, s := range listSessions(t, mux, doomed).Sessions {
		if s.Current {
			doomedID = s.ID
		}
	}

	if w := del(t, mux, "/api/v1/me/sessions/"+doomedID, keeper); w.Code != http.StatusNoContent {
		t.Fatalf("revoking answered %d: %s", w.Code, w.Body)
	}

	// The revoked cookie no longer authenticates anything.
	if w := get(t, mux, "/api/v1/me", doomed); w.Code != http.StatusUnauthorized {
		t.Errorf("the revoked session still authenticates: %d", w.Code)
	}

	if w := get(t, mux, "/api/v1/me", keeper); w.Code != http.StatusOK {
		t.Errorf("revoking one session ended another: %d", w.Code)
	}
}

// A session id that is not the caller's, and one that does not exist, are the
// same 404. Telling them apart would confirm which session ids are real.
func TestAnUnknownOrForeignSessionIsNotFound(t *testing.T) {
	t.Parallel()

	mux := newMux(t, newStore(t))
	cookie := signIn(t, mux)

	for _, id := range []string{
		"0199a1b2-c3d4-7e5f-8a9b-000000009999", // well-formed, nobody's
		"not-a-uuid",                           // not even an id
	} {
		w := del(t, mux, "/api/v1/me/sessions/"+id, cookie)

		if w.Code != http.StatusNotFound {
			t.Errorf("DELETE .../%s answered %d, want 404: %s", id, w.Code, w.Body)
		}
	}

	// And the caller's own session survived all of that.
	if w := get(t, mux, "/api/v1/me", cookie); w.Code != http.StatusOK {
		t.Errorf("the caller's own session was lost: %d", w.Code)
	}
}

// Ending the session making the request is allowed, and the cookie is cleared
// on the way out — otherwise the caller keeps one that authenticates nothing
// and finds out on their next click.
func TestRevokingTheCurrentSessionClearsTheCookie(t *testing.T) {
	t.Parallel()

	mux := newMux(t, newStore(t))
	cookie := signIn(t, mux)

	var id string

	for _, s := range listSessions(t, mux, cookie).Sessions {
		if s.Current {
			id = s.ID
		}
	}

	w := del(t, mux, "/api/v1/me/sessions/"+id, cookie)
	if w.Code != http.StatusNoContent {
		t.Fatalf("revoking answered %d: %s", w.Code, w.Body)
	}

	var cleared bool

	for _, c := range w.Result().Cookies() {
		if c.Name == identityhttp.SessionCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}

	if !cleared {
		t.Error("the session cookie was not cleared after revoking the current session")
	}

	if w := get(t, mux, "/api/v1/me", cookie); w.Code != http.StatusUnauthorized {
		t.Errorf("the revoked current session still authenticates: %d", w.Code)
	}
}

// Signing out everywhere else keeps the caller signed in — an answer they
// cannot receive while still signed in is not what they asked for.
func TestSigningOutElsewhereKeepsTheCallerSignedIn(t *testing.T) {
	t.Parallel()

	mux := newMux(t, newStore(t))

	laptop := signIn(t, mux)
	phone := signIn(t, mux)
	current := signIn(t, mux)

	w := del(t, mux, "/api/v1/me/sessions", current)
	if w.Code != http.StatusOK {
		t.Fatalf("answered %d: %s", w.Code, w.Body)
	}

	var body struct {
		Revoked int `json:"revoked"`
	}

	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not the shape the contract declares: %v", err)
	}

	if body.Revoked != 2 {
		t.Errorf("reported %d revoked, want 2", body.Revoked)
	}

	if w := get(t, mux, "/api/v1/me", current); w.Code != http.StatusOK {
		t.Errorf("the caller was signed out by their own request: %d", w.Code)
	}

	for name, cookie := range map[string]*http.Cookie{"laptop": laptop, "phone": phone} {
		if w := get(t, mux, "/api/v1/me", cookie); w.Code != http.StatusUnauthorized {
			t.Errorf("the %s session survived: %d", name, w.Code)
		}
	}
}

// Every one of these needs a session. Mounted without the guard they would
// answer about whoever the context happened to hold, which is nobody.
func TestTheSessionEndpointsRefuseAnUnauthenticatedCaller(t *testing.T) {
	t.Parallel()

	mux := newMux(t, newStore(t))

	cases := []struct {
		method, path string
	}{
		{http.MethodGet, "/api/v1/me/sessions"},
		{http.MethodDelete, "/api/v1/me/sessions"},
		{http.MethodDelete, "/api/v1/me/sessions/0199a1b2-c3d4-7e5f-8a9b-000000000001"},
	}

	for _, tc := range cases {
		r := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, nil)
		w := httptest.NewRecorder()

		mux.ServeHTTP(w, r)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s answered %d, want 401", tc.method, tc.path, w.Code)
		}
	}
}
