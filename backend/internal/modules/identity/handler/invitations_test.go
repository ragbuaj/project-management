package handler_test

import (
	"context"
	"encoding/json"
	"errors"
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

	"github.com/ragbuaj/project-management/backend/internal/mail"
	identityhttp "github.com/ragbuaj/project-management/backend/internal/modules/identity/handler"
	identityrepo "github.com/ragbuaj/project-management/backend/internal/modules/identity/repository"
	identitysvc "github.com/ragbuaj/project-management/backend/internal/modules/identity/service"
)

// inviteFake is the store half of the invitation service. It answers the
// redemption methods too because the service declares one store for both
// halves; these tests only exercise the sending half.
type inviteFake struct {
	existingAccount bool
	createErr       error
}

func (f *inviteFake) GetUserByEmail(context.Context, string) (identityrepo.GetUserByEmailRow, error) {
	if f.existingAccount {
		return identityrepo.GetUserByEmailRow{ID: "someone"}, nil
	}

	return identityrepo.GetUserByEmailRow{}, pgx.ErrNoRows
}

func (f *inviteFake) ExpireOpenInvitationsForEmail(context.Context, string) (int64, error) {
	return 0, nil
}

func (f *inviteFake) CreateInvitation(_ context.Context, arg identityrepo.CreateInvitationParams) (identityrepo.CreateInvitationRow, error) {
	if f.createErr != nil {
		return identityrepo.CreateInvitationRow{}, f.createErr
	}

	return identityrepo.CreateInvitationRow{
		ID:        "99999999-9999-9999-9999-999999999999",
		Email:     arg.Email,
		Role:      arg.Role,
		InvitedBy: arg.InvitedBy,
		ExpiresAt: arg.ExpiresAt,
	}, nil
}

func (f *inviteFake) GetInvitationByTokenHash(context.Context, []byte) (identityrepo.GetInvitationByTokenHashRow, error) {
	return identityrepo.GetInvitationByTokenHashRow{}, pgx.ErrNoRows
}

func (f *inviteFake) AcceptInvitation(context.Context, string) (int64, error) { return 0, nil }

func (f *inviteFake) CreateUser(context.Context, identityrepo.CreateUserParams) (identityrepo.CreateUserRow, error) {
	return identityrepo.CreateUserRow{}, nil
}

// The password reset half of the store. The module has one transaction boundary
// and therefore one store interface, so an invitation fake has to answer these
// too; no invitation test reaches them.
func (f *inviteFake) ExpireOpenPasswordResetsForUser(context.Context, string) (int64, error) {
	return 0, nil
}

func (f *inviteFake) CreatePasswordReset(context.Context, identityrepo.CreatePasswordResetParams) (identityrepo.CreatePasswordResetRow, error) {
	return identityrepo.CreatePasswordResetRow{}, nil
}

func (f *inviteFake) GetPasswordResetByTokenHash(context.Context, []byte) (identityrepo.GetPasswordResetByTokenHashRow, error) {
	return identityrepo.GetPasswordResetByTokenHashRow{}, pgx.ErrNoRows
}

func (f *inviteFake) UsePasswordReset(context.Context, string) (int64, error) { return 0, nil }

func (f *inviteFake) SetUserPasswordHash(context.Context, identityrepo.SetUserPasswordHashParams) (identityrepo.SetUserPasswordHashRow, error) {
	return identityrepo.SetUserPasswordHashRow{}, pgx.ErrNoRows
}

func (f *inviteFake) DeleteAllSessionsForUser(context.Context, string) (int64, error) {
	return 0, nil
}

func inviteHandler(t *testing.T, store *inviteFake) (*identityhttp.Invitations, *mail.Capture) {
	t.Helper()

	capture := mail.NewCapture(netmail.Address{Name: "PM", Address: "no-reply@pm.example.test"})

	base, err := url.Parse("https://pm.example.test")
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	service := identitysvc.NewInvitations(
		func(ctx context.Context, fn func(identitysvc.TxStore) error) error { return fn(store) },
		capture, base, log, time.Now)

	return identityhttp.NewInvitations(service, identitysvc.NewSessions(newStore(t), log, time.Now), log), capture
}

// invite posts an invitation as somebody with the given account role.
func invite(t *testing.T, h *identityhttp.Invitations, role, body string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/invitations", strings.NewReader(body))
	r = r.WithContext(identityhttp.WithCaller(r.Context(), identitysvc.Authenticated{
		UserID:    "11111111-1111-1111-1111-111111111111",
		Email:     "owner@example.test",
		Name:      "Owner",
		Role:      role,
		SessionID: "22222222-2222-2222-2222-222222222222",
	}))

	w := httptest.NewRecorder()
	h.Create(w, r)

	return w
}

func TestTheOwnerCanInviteAnEmployee(t *testing.T) {
	t.Parallel()

	h, capture := inviteHandler(t, &inviteFake{})

	w := invite(t, h, "owner", `{"email":"budi@example.test","role":"contributor"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body)
	}

	var body struct {
		ID        string    `json:"id"`
		Email     string    `json:"email"`
		Role      string    `json:"role"`
		ExpiresAt time.Time `json:"expires_at"`
	}

	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.Email != "budi@example.test" || body.Role != "contributor" || body.ID == "" {
		t.Errorf("body = %+v", body)
	}

	if body.ExpiresAt.IsZero() {
		t.Error("the response carries no deadline, so nothing can tell the invitee how long they have")
	}

	if len(capture.Sent()) != 1 {
		t.Errorf("%d messages sent, want 1", len(capture.Sent()))
	}
}

// The token exists in the message and nowhere else. A response that carried it
// would put an account-creating credential into a log, a proxy, and whatever
// the owner's browser caches.
func TestTheResponseNeverCarriesTheToken(t *testing.T) {
	t.Parallel()

	h, capture := inviteHandler(t, &inviteFake{})

	w := invite(t, h, "owner", `{"email":"budi@example.test","role":"viewer"}`)

	delivery, ok := capture.Last()
	if !ok {
		t.Fatal("no message was sent")
	}

	_, after, found := strings.Cut(delivery.Message.Text, "/invite/")
	if !found {
		t.Fatalf("no link in:\n%s", delivery.Message.Text)
	}

	token := strings.TrimSpace(strings.SplitN(after, "\n", 2)[0])

	if strings.Contains(w.Body.String(), token) {
		t.Error("the response body carries the invitation token")
	}
}

// docs/authorization.md keeps this on the owner's closed list. Every other
// account role is refused, including maintainer, which is otherwise the highest
// rank that appears in the matrix.
func TestOnlyTheOwnerMayInvite(t *testing.T) {
	t.Parallel()

	for _, role := range []string{"maintainer", "contributor", "viewer", "", "admin"} {
		t.Run(role, func(t *testing.T) {
			t.Parallel()

			h, capture := inviteHandler(t, &inviteFake{})

			w := invite(t, h, role, `{"email":"budi@example.test","role":"viewer"}`)
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d for role %q, want 403", w.Code, role)
			}

			if len(capture.Sent()) != 0 {
				t.Error("a refused caller still had a message sent")
			}
		})
	}
}

// The refusal must come before the body is read, or a role that may not invite
// still gets to say what a valid request looks like.
func TestARefusedCallerNeverReachesTheService(t *testing.T) {
	t.Parallel()

	h, _ := inviteHandler(t, &inviteFake{})

	// Not JSON at all. A handler that decoded first would answer 400 here.
	if w := invite(t, h, "viewer", `not json`); w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 before the body is looked at", w.Code)
	}
}

// A handler mounted without the session middleware is a wiring mistake, not an
// unauthenticated request. Answering 401 would send somebody to the login page
// over and over while their session is perfectly good.
func TestAHandlerReachedWithoutTheSessionMiddlewareFailsAsTheServersFault(t *testing.T) {
	t.Parallel()

	h, _ := inviteHandler(t, &inviteFake{})

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/invitations",
		strings.NewReader(`{"email":"budi@example.test","role":"viewer"}`))
	w := httptest.NewRecorder()

	h.Create(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestTheRequestBodyIsValidated(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"not json":        {body: `{`, want: http.StatusBadRequest},
		"no email":        {body: `{"role":"viewer"}`, want: http.StatusBadRequest},
		"no role":         {body: `{"email":"budi@example.test"}`, want: http.StatusBadRequest},
		"not an address":  {body: `{"email":"budi","role":"viewer"}`, want: http.StatusBadRequest},
		"owner as a role": {body: `{"email":"budi@example.test","role":"owner"}`, want: http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h, capture := inviteHandler(t, &inviteFake{})

			if w := invite(t, h, "owner", tc.body); w.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.want, w.Body)
			}

			if len(capture.Sent()) != 0 {
				t.Error("a refused request still had a message sent")
			}
		})
	}
}

// Not the enumeration risk the login endpoint has: only the owner reaches this,
// and they created every account there is.
func TestAnAddressThatAlreadyHasAnAccountIsAConflict(t *testing.T) {
	t.Parallel()

	h, _ := inviteHandler(t, &inviteFake{existingAccount: true})

	if w := invite(t, h, "owner", `{"email":"budi@example.test","role":"viewer"}`); w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
}

// docs/nfr.md: an invitation nobody receives is not an invitation. Answering
// 201 would leave the owner waiting for an employee who was never written to.
func TestADeliveryFailureIsNotAnswered201(t *testing.T) {
	t.Parallel()

	h, capture := inviteHandler(t, &inviteFake{})
	capture.Fail(errors.New("the mail server hung up"))

	w := invite(t, h, "owner", `{"email":"budi@example.test","role":"viewer"}`)
	if w.Code == http.StatusCreated {
		t.Fatal("a delivery failure was answered 201")
	}

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}

	// The owner has to be able to act on it, and the action is to invite again.
	if !strings.Contains(w.Body.String(), "Undang ulang") {
		t.Errorf("the message does not say what to do: %s", w.Body)
	}
}

// A database that will not answer must not be dressed up as anything the
// caller did, and must not leak what broke.
func TestAStoreFailureIsAnInternalError(t *testing.T) {
	t.Parallel()

	h, _ := inviteHandler(t, &inviteFake{createErr: errors.New("relation does not exist")})

	w := invite(t, h, "owner", `{"email":"budi@example.test","role":"viewer"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}

	if strings.Contains(w.Body.String(), "relation does not exist") {
		t.Errorf("the response leaks the database error: %s", w.Body)
	}
}
