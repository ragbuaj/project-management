// Package handler turns HTTP requests into service calls and service answers
// into HTTP responses. It holds no business rules (ADR-0008).
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

// SessionCookieName carries the __Host- prefix, which a browser only accepts
// on a cookie that is Secure, has Path=/, and names no Domain. That makes the
// cookie unwritable by a sibling subdomain — the one attack a plain session
// cookie cannot defend itself against.
//
// It also means the cookie is never sent over plain HTTP. Browsers treat
// localhost as a secure context, so local development still works.
const SessionCookieName = "__Host-session"

// maxLoginBody bounds the request body. Credentials are small; anything larger
// is a client that will not be helped by having its megabyte parsed.
const maxLoginBody = 4 << 10

// Auth serves the endpoints in docs/api/openapi.yaml under the auth tag.
type Auth struct {
	credentials *identitysvc.Credentials
	sessions    *identitysvc.Sessions
	log         *slog.Logger
}

func NewAuth(credentials *identitysvc.Credentials, sessions *identitysvc.Sessions, log *slog.Logger) *Auth {
	return &Auth{credentials: credentials, sessions: sessions, log: log}
}

// userBody is the User schema of the contract. The field names are the
// contract's, and they are snake_case because that is what it declares.
type userBody struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Timezone string `json:"timezone"`
	IsOwner  bool   `json:"is_owner"`
}

func bodyOf(user identitysvc.User) userBody {
	return userBody{
		ID:       user.ID,
		Email:    user.Email,
		Name:     user.Name,
		Timezone: user.Timezone,
		IsOwner:  user.IsOwner,
	}
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	User userBody `json:"user"`
}

// Login checks the credentials, starts a session, and sets the cookie.
func (a *Auth) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest

	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoginBody)).Decode(&req); err != nil {
		httpx.WriteError(w, r, httpx.CodeValidationFailed, "Isi permintaan tidak bisa dibaca sebagai JSON.")

		return
	}

	// Presence only. Anything more specific — a rule about the address, a rule
	// about the password — would answer a question the caller is not entitled
	// to ask on this endpoint.
	var missing []httpx.FieldError

	if req.Email == "" {
		missing = append(missing, httpx.FieldError{Field: "email", Code: "REQUIRED"})
	}

	if req.Password == "" {
		missing = append(missing, httpx.FieldError{Field: "password", Code: "REQUIRED"})
	}

	if len(missing) > 0 {
		httpx.WriteError(w, r, httpx.CodeValidationFailed, "Email dan password wajib diisi.", missing...)

		return
	}

	user, err := a.credentials.Authenticate(r.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, identitysvc.ErrInvalidCredentials) {
			// One message for both halves. docs/api/openapi.yaml declares this
			// response as identical for an unregistered address and a wrong
			// password, and the wording is part of that promise.
			httpx.WriteError(w, r, httpx.CodeUnauthenticated, "Email atau password salah.")

			return
		}

		httpx.WriteInternalError(w, r, a.log, err)

		return
	}

	token, session, err := a.sessions.Issue(r.Context(), user.ID, r.UserAgent())
	if err != nil {
		httpx.WriteInternalError(w, r, a.log, err)

		return
	}

	setSessionCookie(w, token, session.ExpiresAt)

	httpx.WriteJSON(w, http.StatusOK, loginResponse{User: bodyOf(user)})
}

// Logout ends the session the cookie names and clears the cookie.
//
// It answers 204 whether or not there was a session to end. A caller who has
// been signed out already, or who never was, still wants the same thing to be
// true afterwards, and telling them apart would only say whether the cookie
// they hold is real.
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(SessionCookieName)
	if err == nil {
		if err := a.sessions.Revoke(r.Context(), cookie.Value); err != nil {
			// Reported rather than swallowed: the session is still live, and
			// answering 204 would tell someone they are signed out when they
			// are not.
			httpx.WriteInternalError(w, r, a.log, err)

			return
		}
	}

	clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// setSessionCookie writes the cookie ADR-0005 specifies.
//
// SameSite=Lax rather than Strict is deliberate and explained in ADR-0005:
// Strict breaks opening a card link from Telegram or from a code review, which
// would land a signed-in person on the login page.
func setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearSessionCookie overwrites the cookie with an expired one. The attributes
// have to match the ones it was set with, or the browser keeps both.
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}
