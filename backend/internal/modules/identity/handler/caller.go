package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/ragbuaj/project-management/backend/internal/httpx"
	identitysvc "github.com/ragbuaj/project-management/backend/internal/modules/identity/service"
)

// errNoCaller means a handler that needs an identity was mounted without the
// middleware that resolves one. It is a wiring mistake, so it is reported as
// the server's fault rather than the caller's.
var errNoCaller = errors.New("handler reached without the session middleware")

// callerKey is unexported and of a private type, so nothing outside this
// package can write a caller into a context. An identity that any package
// could plant is an identity no handler can trust.
type callerKey struct{}

// WithCaller attaches the authenticated caller to ctx. Only the session
// middleware is expected to call it.
func WithCaller(ctx context.Context, who identitysvc.Authenticated) context.Context {
	return context.WithValue(ctx, callerKey{}, who)
}

// CallerFrom returns the caller the session middleware resolved.
//
// The second result is false when the handler was reached without that
// middleware. That is a wiring mistake rather than an unauthenticated request,
// and the two must not be confused: answering 401 would send someone to the
// login page over and over while their session is perfectly good.
func CallerFrom(ctx context.Context) (identitysvc.Authenticated, bool) {
	who, ok := ctx.Value(callerKey{}).(identitysvc.Authenticated)

	return who, ok
}

// Me answers with the caller's own identity.
//
// The fields are read from the database on the way in rather than carried in
// the cookie, so a change of name, timezone, or role shows up on the next
// request instead of at the next expiry. That is what ADR-0005 chose opaque
// sessions for.
func (a *Auth) Me(w http.ResponseWriter, r *http.Request) {
	who, ok := CallerFrom(r.Context())
	if !ok {
		httpx.WriteInternalError(w, r, a.log, errNoCaller)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, userBody{
		ID:       who.UserID,
		Email:    who.Email,
		Name:     who.Name,
		Timezone: who.Timezone,
		IsOwner:  who.IsOwner,
	})
}
