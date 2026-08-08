package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/ragbuaj/project-management/backend/internal/httpx"
	identitydom "github.com/ragbuaj/project-management/backend/internal/modules/identity/domain"
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

type confirmPasswordResetRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// Confirm sets the new password and ends every session the account had.
//
// No session is required to reach it, and none is issued by it. Redeeming an
// invitation signs the new employee in because they have just chosen their first
// password and there is nothing else they could be doing; a reset is different
// on purpose (keputusan pemilik, 2026-08-08) — the caller signs in afterwards
// with the password they just set, through the endpoint that counts failed
// attempts.
//
// The session cookie is cleared even though the caller usually has none. Every
// session for the account was deleted server-side a moment ago, so a browser
// that still holds one holds a dead credential, and the next request would be a
// 401 nobody could explain.
func (p *PasswordResets) Confirm(w http.ResponseWriter, r *http.Request) {
	var req confirmPasswordResetRequest

	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPasswordResetBody)).Decode(&req); err != nil {
		httpx.WriteError(w, r, httpx.CodeValidationFailed, "Isi permintaan tidak bisa dibaca sebagai JSON.")

		return
	}

	var missing []httpx.FieldError

	for _, f := range []struct {
		name  string
		value string
	}{
		{"token", req.Token},
		{"password", req.Password},
	} {
		if f.value == "" {
			missing = append(missing, httpx.FieldError{Field: f.name, Code: "REQUIRED"})
		}
	}

	if len(missing) > 0 {
		httpx.WriteError(w, r, httpx.CodeValidationFailed, "Tautan dan sandi baru wajib diisi.", missing...)

		return
	}

	if err := p.resets.Confirm(r.Context(), req.Token, req.Password); err != nil {
		p.writeConfirmError(w, r, err)

		return
	}

	clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// writeConfirmError turns the service's refusals into the contract's answers.
//
// The password rules are the deliberate exception to saying as little as
// possible: they are something the caller can still fix, and a form that refuses
// a password without saying why is a form nobody gets past. It is the same
// exception /invitations/accept makes, for the same reason.
func (p *PasswordResets) writeConfirmError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, identitysvc.ErrPasswordResetNotUsable):
		// One answer to a link that was never issued, one that expired, one
		// already used, one that is not shaped like a token, and one whose
		// account has since been masked. The log knows which it was.
		httpx.WriteError(w, r, httpx.CodeNotFound,
			"Tautan ini tidak berlaku. Minta tautan baru lewat halaman lupa sandi.")

	case errors.Is(err, identitydom.ErrPasswordTooShort):
		httpx.WriteError(w, r, httpx.CodeValidationFailed,
			fmt.Sprintf("Sandi minimal %d karakter.", identitydom.MinPasswordLength),
			httpx.FieldError{Field: "password", Code: "TOO_SHORT"})

	case errors.Is(err, identitydom.ErrPasswordTooLong):
		httpx.WriteError(w, r, httpx.CodeValidationFailed,
			fmt.Sprintf("Sandi maksimal %d byte.", identitydom.MaxPasswordLength),
			httpx.FieldError{Field: "password", Code: "TOO_LONG"})

	default:
		httpx.WriteInternalError(w, r, p.log, err)
	}
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
