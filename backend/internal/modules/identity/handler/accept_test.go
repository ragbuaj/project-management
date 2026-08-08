package handler_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	netmail "net/mail"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ragbuaj/project-management/backend/internal/mail"
	identitydom "github.com/ragbuaj/project-management/backend/internal/modules/identity/domain"
	identityhttp "github.com/ragbuaj/project-management/backend/internal/modules/identity/handler"
	identityrepo "github.com/ragbuaj/project-management/backend/internal/modules/identity/repository"
	identitysvc "github.com/ragbuaj/project-management/backend/internal/modules/identity/service"
)

const chosenPassword = "sandi-yang-cukup-panjang"

// acceptFake answers the redemption half of the store.
type acceptFake struct {
	inviteFake

	invitation *identityrepo.GetInvitationByTokenHashRow
	lostRace   bool
	duplicate  bool
}

func (f *acceptFake) GetInvitationByTokenHash(context.Context, []byte) (identityrepo.GetInvitationByTokenHashRow, error) {
	if f.invitation == nil {
		return identityrepo.GetInvitationByTokenHashRow{}, pgx.ErrNoRows
	}

	return *f.invitation, nil
}

func (f *acceptFake) AcceptInvitation(context.Context, string) (int64, error) {
	if f.lostRace {
		return 0, nil
	}

	return 1, nil
}

func (f *acceptFake) CreateUser(_ context.Context, arg identityrepo.CreateUserParams) (identityrepo.CreateUserRow, error) {
	if f.duplicate {
		// What users_email_key raises when the address already has an account.
		return identityrepo.CreateUserRow{}, &pgconn.PgError{Code: "23505", ConstraintName: "users_email_key"}
	}

	return identityrepo.CreateUserRow{
		ID:       "0199a1b2-c3d4-7e5f-8a9b-000000000042",
		Email:    arg.Email,
		Name:     arg.Name,
		Timezone: "Asia/Jakarta",
		Role:     arg.Role,
	}, nil
}

// openLink mints a token and the open invitation that carries it.
func openLink(t *testing.T) (string, *identityrepo.GetInvitationByTokenHashRow) {
	t.Helper()

	token, _, err := identitydom.NewInvitationToken()
	if err != nil {
		t.Fatalf("new invitation token: %v", err)
	}

	return token, &identityrepo.GetInvitationByTokenHashRow{
		ID:        "0199a1b2-c3d4-7e5f-8a9b-000000000007",
		Email:     "budi@example.test",
		Role:      "contributor",
		InvitedBy: "0199a1b2-c3d4-7e5f-8a9b-000000000001",
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(24 * time.Hour), Valid: true},
	}
}

// acceptHandler returns the handler and the session store behind it, so a test
// can ask whether a session was created at all -- a refusal that still wrote a
// session row is invisible in the response, whose status is already committed.
func acceptHandler(t *testing.T, store *acceptFake) (*identityhttp.Invitations, *store) {
	t.Helper()

	base, err := url.Parse("https://pm.example.test")
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	service := identitysvc.NewInvitations(
		func(_ context.Context, fn func(identitysvc.TxStore) error) error { return fn(store) },
		mail.NewCapture(netmail.Address{Name: "PM", Address: "no-reply@pm.example.test"}),
		base, log, time.Now)

	sessionStore := newStore(t)

	return identityhttp.NewInvitations(service, identitysvc.NewSessions(sessionStore, log, time.Now), log), sessionStore
}

func accept(t *testing.T, h *identityhttp.Invitations, body string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/invitations/accept",
		strings.NewReader(body))
	w := httptest.NewRecorder()

	h.Accept(w, r)

	return w
}

// redemption builds the request body out of its three parts, so no test has to
// hand-assemble JSON around a token.
func redemption(token, name, password string) string {
	body, err := json.Marshal(map[string]string{
		"token":    token,
		"name":     name,
		"password": password,
	})
	if err != nil {
		panic(err)
	}

	return string(body)
}

func TestFollowingALinkCreatesTheAccountAndSignsThePersonIn(t *testing.T) {
	t.Parallel()

	token, invitation := openLink(t)
	h, _ := acceptHandler(t, &acceptFake{invitation: invitation})

	w := accept(t, h, redemption(token, "Budi Santoso", chosenPassword))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body)
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
		t.Fatalf("decode: %v", err)
	}

	// The address and the role come from the invitation. Only the name came
	// from the request, which is what stops an invitation to one address from
	// being a way to create an account on any other.
	if body.User.Email != "budi@example.test" || body.User.Role != "contributor" {
		t.Errorf("user = %+v, want the invitation's address and role", body.User)
	}

	if body.User.Name != "Budi Santoso" || body.User.Timezone != "Asia/Jakarta" {
		t.Errorf("user = %+v", body.User)
	}

	// Signed in: somebody who has just proved they hold the link and chosen
	// their own password has done everything logging in would ask of them.
	var session *http.Cookie

	for _, c := range w.Result().Cookies() {
		if c.Name == identityhttp.SessionCookieName {
			session = c
		}
	}

	if session == nil {
		t.Fatal("no session cookie was set")
	}

	if !session.HttpOnly || !session.Secure || session.SameSite != http.SameSiteLaxMode {
		t.Errorf("session cookie = %+v, want HttpOnly, Secure and SameSite=Lax", session)
	}
}

// Four different facts, one answer. Telling them apart would confirm to
// whoever holds the link that the address was invited.
func TestALinkThatCannotBeUsedIsOneAnswerAndNoSession(t *testing.T) {
	t.Parallel()

	usable, invitation := openLink(t)

	expired := *invitation
	expired.ExpiresAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}

	redeemed := *invitation
	redeemed.AcceptedAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}

	unknown, _ := openLink(t)

	for name, tc := range map[string]struct {
		token string
		store *acceptFake
	}{
		"malformed":        {token: "not-a-token", store: &acceptFake{}},
		"never issued":     {token: unknown, store: &acceptFake{}},
		"expired":          {token: usable, store: &acceptFake{invitation: &expired}},
		"already redeemed": {token: usable, store: &acceptFake{invitation: &redeemed}},
		"lost the race":    {token: usable, store: &acceptFake{invitation: invitation, lostRace: true}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h, sessions := acceptHandler(t, tc.store)

			w := accept(t, h, redemption(tc.token, "Budi", chosenPassword))
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404: %s", w.Code, w.Body)
			}

			if len(w.Result().Cookies()) != 0 {
				t.Error("a refused redemption still set a cookie")
			}

			// The status is already committed by the time a stray
			// setSessionCookie would run, so the response cannot show a missing
			// return. The session row can.
			if len(sessions.sessions) != 0 {
				t.Error("a refused redemption still created a session")
			}
		})
	}
}

// The password rules are the deliberate exception to saying as little as
// possible: they are something the caller can still fix, and a form that
// refuses a password without saying why is a form nobody gets past.
func TestThePasswordPolicyIsExplainedRatherThanHidden(t *testing.T) {
	t.Parallel()

	token, invitation := openLink(t)

	for name, tc := range map[string]struct {
		password string
		wantCode string
	}{
		"too short": {password: "pendek", wantCode: "TOO_SHORT"},
		"too long":  {password: strings.Repeat("a", 1025), wantCode: "TOO_LONG"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h, _ := acceptHandler(t, &acceptFake{invitation: invitation})

			w := accept(t, h, redemption(token, "Budi", tc.password))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", w.Code, w.Body)
			}

			if !strings.Contains(w.Body.String(), tc.wantCode) {
				t.Errorf("body does not carry %s: %s", tc.wantCode, w.Body)
			}
		})
	}
}

func TestTheRedemptionBodyIsValidated(t *testing.T) {
	t.Parallel()

	token, invitation := openLink(t)

	// The field code is asserted, not just the status. A field left out and a
	// field filled with spaces are different mistakes, and a form that reports
	// them the same way sends somebody hunting for the wrong thing.
	for name, tc := range map[string]struct {
		body     string
		wantCode string
	}{
		"not json":    {body: `{`},
		"no token":    {body: redemption("", "Budi", chosenPassword), wantCode: `"field":"token","code":"REQUIRED"`},
		"no name":     {body: redemption(token, "", chosenPassword), wantCode: `"field":"name","code":"REQUIRED"`},
		"no password": {body: redemption(token, "Budi", ""), wantCode: `"field":"password","code":"REQUIRED"`},
		"blank name":  {body: redemption(token, "   ", chosenPassword), wantCode: `"field":"name","code":"INVALID"`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h, _ := acceptHandler(t, &acceptFake{invitation: invitation})

			w := accept(t, h, tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", w.Code, w.Body)
			}

			if tc.wantCode != "" && !strings.Contains(w.Body.String(), tc.wantCode) {
				t.Errorf("body does not carry %s: %s", tc.wantCode, w.Body)
			}
		})
	}
}

// A password and an account-creating token are both in the request body. If
// either reached the response it would sit in every proxy log between here and
// the browser.
func TestTheRedemptionResponseNeverCarriesTheSecrets(t *testing.T) {
	t.Parallel()

	token, invitation := openLink(t)
	h, _ := acceptHandler(t, &acceptFake{invitation: invitation})

	w := accept(t, h, redemption(token, "Budi", chosenPassword))

	if strings.Contains(w.Body.String(), chosenPassword) {
		t.Error("the response body carries the password")
	}

	if strings.Contains(w.Body.String(), token) {
		t.Error("the response body carries the invitation token")
	}
}

// The address gained an account between the send and the click. The address is
// not echoed back: whoever holds this link is not necessarily the person it was
// sent to.
func TestAnAddressThatGainedAnAccountMeanwhileIsAConflict(t *testing.T) {
	t.Parallel()

	token, invitation := openLink(t)
	store := &acceptFake{invitation: invitation}
	store.duplicate = true

	h, sessions := acceptHandler(t, store)

	w := accept(t, h, redemption(token, "Budi", chosenPassword))
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", w.Code, w.Body)
	}

	if strings.Contains(w.Body.String(), invitation.Email) {
		t.Errorf("the response echoes the invited address: %s", w.Body)
	}

	if len(sessions.sessions) != 0 {
		t.Error("a refused redemption still created a session")
	}
}
