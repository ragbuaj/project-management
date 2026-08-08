package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/ragbuaj/project-management/backend/internal/httpx"
	identitysvc "github.com/ragbuaj/project-management/backend/internal/modules/identity/service"
)

// maxPasswordResetBody bounds the request body. An address and nothing else.
const maxPasswordResetBody = 4 << 10

// PasswordResets serves the endpoints under the auth tag that let somebody who
// has forgotten their password choose a new one.
type PasswordResets struct {
	resets *identitysvc.PasswordResets
	log    *slog.Logger
}

func NewPasswordResets(resets *identitysvc.PasswordResets, log *slog.Logger) *PasswordResets {
	return &PasswordResets{resets: resets, log: log}
}

type requestPasswordResetRequest struct {
	Email string `json:"email"`
}

// Request asks for a reset link.
//
// **It answers 202 whether or not the address has an account**, and that is the
// whole shape of the endpoint rather than a detail of it. No session is
// required — somebody who cannot sign in is exactly who this is for — so an
// endpoint that reported "no such address" would be an address checker anybody
// could call. docs/threat-model.md closes enumeration here.
//
// 202 rather than 200 or 204 because it is honest: what has been accepted is
// the request, and whether a message goes out is deliberately not something the
// caller is told.
func (p *PasswordResets) Request(w http.ResponseWriter, r *http.Request) {
	var req requestPasswordResetRequest

	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPasswordResetBody)).Decode(&req); err != nil {
		httpx.WriteError(w, r, httpx.CodeValidationFailed, "Isi permintaan tidak bisa dibaca sebagai JSON.")

		return
	}

	if req.Email == "" {
		httpx.WriteError(w, r, httpx.CodeValidationFailed, "Email wajib diisi.",
			httpx.FieldError{Field: "email", Code: "REQUIRED"})

		return
	}

	retryAfter, err := p.resets.Request(r.Context(), req.Email)
	if err != nil {
		p.writeRequestError(w, r, retryAfter, err)

		return
	}

	w.WriteHeader(http.StatusAccepted)
}

// writeRequestError turns the service's refusals into the contract's answers.
//
// There is no case here for a delivery failure, and there cannot be: the
// service swallows it into the log on purpose. A 500 for an undeliverable
// message would arrive only when the address has an account, which hands back
// the enumeration the constant 202 exists to deny. Invitations answer 500 for
// the same failure because only the owner ever sees it.
func (p *PasswordResets) writeRequestError(w http.ResponseWriter, r *http.Request, retryAfter time.Duration, err error) {
	switch {
	case errors.Is(err, identitysvc.ErrTooManyAttempts):
		httpx.WriteRateLimited(w, r, retryAfter)

	case errors.Is(err, identitysvc.ErrInvalidEmail):
		// Says nothing about whether an account exists — only that what was
		// submitted is not an address at all, which the caller can see for
		// themselves.
		httpx.WriteError(w, r, httpx.CodeValidationFailed, "Alamat email tidak valid.",
			httpx.FieldError{Field: "email", Code: "INVALID"})

	default:
		httpx.WriteInternalError(w, r, p.log, err)
	}
}
