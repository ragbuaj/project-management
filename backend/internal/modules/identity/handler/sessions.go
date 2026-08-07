package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/ragbuaj/project-management/backend/internal/httpx"
	identitysvc "github.com/ragbuaj/project-management/backend/internal/modules/identity/service"
)

// sessionBody is the Session schema of the contract.
//
// There is no token here and no field that could hold one. What this screen is
// for is recognizing a session, and none of that needs the credential — see the
// note on identitysvc.SessionSummary.
type sessionBody struct {
	ID         string    `json:"id"`
	UserAgent  string    `json:"user_agent"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Current    bool      `json:"current"`
}

type sessionListResponse struct {
	Sessions []sessionBody `json:"sessions"`
}

type revokedResponse struct {
	Revoked int64 `json:"revoked"`
}

// ListSessions answers with the caller's own live sessions.
//
// The caller's id comes from the session middleware, never from the request.
// docs/authorization.md keeps sessions to their owner even for `owner` — the
// only row in that matrix with no owner exception — so there is deliberately no
// way to ask this endpoint about anybody else.
func (a *Auth) ListSessions(w http.ResponseWriter, r *http.Request) {
	who, ok := CallerFrom(r.Context())
	if !ok {
		httpx.WriteInternalError(w, r, a.log, errNoCaller)

		return
	}

	sessions, err := a.sessions.List(r.Context(), who.UserID, who.SessionID)
	if err != nil {
		httpx.WriteInternalError(w, r, a.log, err)

		return
	}

	body := sessionListResponse{Sessions: make([]sessionBody, 0, len(sessions))}

	for _, s := range sessions {
		body.Sessions = append(body.Sessions, sessionBody{
			ID:         s.ID,
			UserAgent:  s.UserAgent,
			CreatedAt:  s.CreatedAt,
			LastSeenAt: s.LastSeenAt,
			ExpiresAt:  s.ExpiresAt,
			Current:    s.Current,
		})
	}

	httpx.WriteJSON(w, http.StatusOK, body)
}

// RevokeSession ends one of the caller's sessions.
//
// A session that does not exist and one that belongs to somebody else are the
// same 404, which is what the rest of this API does with resources that are not
// the caller's: telling them apart would confirm which session ids are real.
//
// Revoking the session making the request is allowed, and clears the cookie on
// the way out — otherwise the caller keeps a cookie that authenticates nothing
// and finds out on their next click.
func (a *Auth) RevokeSession(w http.ResponseWriter, r *http.Request) {
	who, ok := CallerFrom(r.Context())
	if !ok {
		httpx.WriteInternalError(w, r, a.log, errNoCaller)

		return
	}

	id := r.PathValue("id")

	if err := a.sessions.RevokeByID(r.Context(), who.UserID, id); err != nil {
		if errors.Is(err, identitysvc.ErrSessionNotFound) {
			httpx.WriteError(w, r, httpx.CodeNotFound, "Sesi tidak ditemukan.")

			return
		}

		httpx.WriteInternalError(w, r, a.log, err)

		return
	}

	if id == who.SessionID {
		clearSessionCookie(w)
	}

	w.WriteHeader(http.StatusNoContent)
}

// RevokeOtherSessions ends every session except the one making the request.
//
// It stops short of the current one on purpose, and that is not an oversight
// dressed up as a decision: POST /auth/logout already ends this session, so a
// caller who wants to be signed out everywhere including here calls both. One
// endpoint that signed the caller out while still owing them an answer would be
// the worse half of that pair.
//
// The count is returned because it is the only way the caller learns anything
// happened. "Signed out 3 other devices" is the confirmation somebody who has
// just lost a laptop is looking for.
func (a *Auth) RevokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	who, ok := CallerFrom(r.Context())
	if !ok {
		httpx.WriteInternalError(w, r, a.log, errNoCaller)

		return
	}

	revoked, err := a.sessions.RevokeOthers(r.Context(), who.UserID, who.SessionID)
	if err != nil {
		httpx.WriteInternalError(w, r, a.log, err)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, revokedResponse{Revoked: revoked})
}
