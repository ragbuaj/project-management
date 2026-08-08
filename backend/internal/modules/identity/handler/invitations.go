package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/ragbuaj/project-management/backend/internal/authz"
	"github.com/ragbuaj/project-management/backend/internal/httpx"
	identitydom "github.com/ragbuaj/project-management/backend/internal/modules/identity/domain"
	identitysvc "github.com/ragbuaj/project-management/backend/internal/modules/identity/service"
)

// maxInvitationBody bounds the request body. An address, a role, and nothing
// else.
const maxInvitationBody = 4 << 10

// Invitations serves the endpoints under the invitations tag.
type Invitations struct {
	invitations *identitysvc.Invitations
	// sessions is here because redeeming a link signs the new employee in. See
	// Accept for why that is not left to the login page.
	sessions *identitysvc.Sessions
	log      *slog.Logger
}

func NewInvitations(invitations *identitysvc.Invitations, sessions *identitysvc.Sessions, log *slog.Logger) *Invitations {
	return &Invitations{invitations: invitations, sessions: sessions, log: log}
}

type createInvitationRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

// invitationBody is the Invitation schema of the contract. The token is not
// among the fields, and cannot be: it exists in the message and nowhere else.
type invitationBody struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Create invites somebody who has no account yet.
//
// Only the owner reaches this. Self-registration is a non-goal, so this is the
// one way an account is ever created, and docs/authorization.md keeps it on the
// closed list of rights held outside any membership (ADR-0012).
func (i *Invitations) Create(w http.ResponseWriter, r *http.Request) {
	who, ok := CallerFrom(r.Context())
	if !ok {
		httpx.WriteInternalError(w, r, i.log, errNoCaller)

		return
	}

	// The role is parsed from what the database stored, so a row carrying
	// something authz does not know is a broken account rather than a caller
	// who may invite.
	role, err := authz.ParseRole(who.Role)
	if err != nil || !authz.Can(authz.Caller{UserID: who.UserID, Role: role}, authz.ActionUserInvite) {
		httpx.WriteError(w, r, httpx.CodeForbidden, "Hanya owner yang bisa mengundang pengguna.")

		return
	}

	var req createInvitationRequest

	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxInvitationBody)).Decode(&req); err != nil {
		httpx.WriteError(w, r, httpx.CodeValidationFailed, "Isi permintaan tidak bisa dibaca sebagai JSON.")

		return
	}

	var missing []httpx.FieldError

	if req.Email == "" {
		missing = append(missing, httpx.FieldError{Field: "email", Code: "REQUIRED"})
	}

	if req.Role == "" {
		missing = append(missing, httpx.FieldError{Field: "role", Code: "REQUIRED"})
	}

	if len(missing) > 0 {
		httpx.WriteError(w, r, httpx.CodeValidationFailed, "Email dan peran wajib diisi.", missing...)

		return
	}

	invited, err := i.invitations.Create(r.Context(), who.UserID, req.Email, req.Role)
	if err != nil {
		i.writeCreateError(w, r, invited, err)

		return
	}

	httpx.WriteJSON(w, http.StatusCreated, invitationBody{
		ID:        invited.ID,
		Email:     invited.Email,
		Role:      invited.Role,
		ExpiresAt: invited.ExpiresAt,
	})
}

// writeCreateError turns the service's refusals into the contract's answers.
//
// The delivery failure is the interesting one. docs/nfr.md refuses to let it be
// swallowed — an invitation nobody receives is not an invitation — so answering
// 201 is out: it would leave the owner waiting for an employee who was never
// written to. It is a 500 whose message says both true things at once, that the
// invitation was recorded and that the message did not go, and what to do about
// it. The invitation's id goes to the log rather than to the body: the caller
// already knows the address it just submitted, and the standard error envelope
// is not worth a second shape for one case.
func (i *Invitations) writeCreateError(w http.ResponseWriter, r *http.Request, invited identitysvc.Invited, err error) {
	switch {
	case errors.Is(err, identitysvc.ErrInvalidEmail):
		httpx.WriteError(w, r, httpx.CodeValidationFailed, "Alamat email tidak valid.",
			httpx.FieldError{Field: "email", Code: "INVALID"})

	case errors.Is(err, identitysvc.ErrRoleNotInvitable):
		httpx.WriteError(w, r, httpx.CodeValidationFailed, "Peran itu tidak bisa diundang.",
			httpx.FieldError{Field: "role", Code: "INVALID"})

	case errors.Is(err, identitysvc.ErrEmailTaken):
		// Not the enumeration risk the login endpoint has: only the owner ever
		// sees this, and the owner is entitled to know who already has an
		// account — they are the one who created every one of them.
		httpx.WriteError(w, r, httpx.CodeConflict, "Alamat itu sudah punya akun. Tambahkan orangnya langsung ke folder atau project.")

	case errors.Is(err, identitysvc.ErrUndeliverable):
		i.log.ErrorContext(r.Context(), "invitation could not be delivered",
			slog.String("request_id", httpx.RequestIDFrom(r.Context())),
			slog.String("invitation_id", invited.ID),
			slog.String("error", err.Error()))

		httpx.WriteError(w, r, httpx.CodeInternal,
			"Undangan tercatat tapi emailnya gagal dikirim. Undang ulang untuk mengirim tautan baru.")

	default:
		httpx.WriteInternalError(w, r, i.log, err)
	}
}

// maxAcceptBody bounds the redemption body. A token, a name, and a password —
// the password alone is capped at 1024 bytes by ADR-0009.
const maxAcceptBody = 8 << 10

type acceptInvitationRequest struct {
	Token    string `json:"token"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

type acceptInvitationResponse struct {
	User userBody `json:"user"`
}

// Accept turns a link into an account and signs the new employee in.
//
// No session is required to reach it: the person holding the link has no
// account yet, which is the whole point. What stands in for a session is the
// token, and it is single-use — see identitysvc.Invitations.Accept.
//
// The session is issued here rather than left to a redirect to the login page.
// Somebody who has just proved they hold the link and chosen their own password
// has done everything logging in would ask of them, and sending them to a form
// to type the password they set ten seconds ago is a step that protects nobody.
func (i *Invitations) Accept(w http.ResponseWriter, r *http.Request) {
	var req acceptInvitationRequest

	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAcceptBody)).Decode(&req); err != nil {
		httpx.WriteError(w, r, httpx.CodeValidationFailed, "Isi permintaan tidak bisa dibaca sebagai JSON.")

		return
	}

	var missing []httpx.FieldError

	for _, f := range []struct {
		name  string
		value string
	}{
		{"token", req.Token},
		{"name", req.Name},
		{"password", req.Password},
	} {
		if f.value == "" {
			missing = append(missing, httpx.FieldError{Field: f.name, Code: "REQUIRED"})
		}
	}

	if len(missing) > 0 {
		httpx.WriteError(w, r, httpx.CodeValidationFailed, "Tautan, nama, dan sandi wajib diisi.", missing...)

		return
	}

	user, err := i.invitations.Accept(r.Context(), req.Token, req.Name, req.Password)
	if err != nil {
		i.writeAcceptError(w, r, err)

		return
	}

	token, session, err := i.sessions.Issue(r.Context(), user.ID, r.UserAgent())
	if err != nil {
		// The account exists. Reporting this as a failure is still right: the
		// caller cannot tell that it was created, and the way out is to sign in
		// with the password they just chose.
		httpx.WriteInternalError(w, r, i.log, err)

		return
	}

	setSessionCookie(w, token, session.ExpiresAt)

	httpx.WriteJSON(w, http.StatusCreated, acceptInvitationResponse{User: bodyOf(user)})
}

// writeAcceptError turns the service's refusals into the contract's answers.
//
// The password rules are the deliberate exception to saying as little as
// possible: they are something the caller can still fix, and a form that
// refuses a password without saying why is a form nobody gets past.
func (i *Invitations) writeAcceptError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, identitysvc.ErrInvitationNotUsable):
		// One answer to a link that was never issued, one that expired, one
		// already redeemed, and one that is not shaped like a token. Telling
		// them apart would confirm to whoever holds the link that the address
		// was invited (docs/threat-model.md). The log knows which it was.
		httpx.WriteError(w, r, httpx.CodeNotFound,
			"Tautan undangan ini tidak berlaku. Minta undangan baru ke owner.")

	case errors.Is(err, identitydom.ErrPasswordTooShort):
		httpx.WriteError(w, r, httpx.CodeValidationFailed,
			fmt.Sprintf("Sandi minimal %d karakter.", identitydom.MinPasswordLength),
			httpx.FieldError{Field: "password", Code: "TOO_SHORT"})

	case errors.Is(err, identitydom.ErrPasswordTooLong):
		httpx.WriteError(w, r, httpx.CodeValidationFailed,
			fmt.Sprintf("Sandi maksimal %d byte.", identitydom.MaxPasswordLength),
			httpx.FieldError{Field: "password", Code: "TOO_LONG"})

	case errors.Is(err, identitysvc.ErrInvalidName):
		httpx.WriteError(w, r, httpx.CodeValidationFailed, "Nama tidak bisa disimpan.",
			httpx.FieldError{Field: "name", Code: "INVALID"})

	case errors.Is(err, identitysvc.ErrEmailTaken):
		// The address gained an account between the send and the click. The
		// address is not echoed: whoever is holding this link is not
		// necessarily the person it was sent to.
		httpx.WriteError(w, r, httpx.CodeConflict,
			"Alamat pada undangan ini sudah punya akun. Silakan masuk.")

	default:
		httpx.WriteInternalError(w, r, i.log, err)
	}
}
