package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/ragbuaj/project-management/backend/internal/authz"
	"github.com/ragbuaj/project-management/backend/internal/httpx"
	identitysvc "github.com/ragbuaj/project-management/backend/internal/modules/identity/service"
)

// maxInvitationBody bounds the request body. An address, a role, and nothing
// else.
const maxInvitationBody = 4 << 10

// Invitations serves the endpoints under the invitations tag.
type Invitations struct {
	invitations *identitysvc.Invitations
	log         *slog.Logger
}

func NewInvitations(invitations *identitysvc.Invitations, log *slog.Logger) *Invitations {
	return &Invitations{invitations: invitations, log: log}
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
