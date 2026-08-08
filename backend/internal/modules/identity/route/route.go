// Package route registers the identity endpoints and the middleware that only
// they need (ADR-0008).
package route

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/ragbuaj/project-management/backend/internal/httpx"
	identityhttp "github.com/ragbuaj/project-management/backend/internal/modules/identity/handler"
	identitysvc "github.com/ragbuaj/project-management/backend/internal/modules/identity/service"
)

// Register mounts the auth endpoints on mux under the prefix the contract
// declares as its server URL.
//
// RequireSession is applied here rather than inside each handler. A handler
// that has to remember to check is a handler that will one day be added
// without checking, and the mistake is invisible until somebody reads data
// that was never theirs.
func Register(
	mux *http.ServeMux,
	auth *identityhttp.Auth,
	invitations *identityhttp.Invitations,
	resets *identityhttp.PasswordResets,
	sessions *identitysvc.Sessions,
	inviteLimit httpx.Middleware,
	acceptLimit httpx.Middleware,
	resetRequestLimit httpx.Middleware,
	log *slog.Logger,
) {
	guard := RequireSession(sessions, log)

	mux.Handle("POST /api/v1/auth/login", http.HandlerFunc(auth.Login))
	mux.Handle("POST /api/v1/auth/logout", http.HandlerFunc(auth.Logout))
	mux.Handle("GET /api/v1/me", guard(http.HandlerFunc(auth.Me)))

	// Every one of these is behind the guard, and every one of them answers
	// about the caller and nobody else. docs/authorization.md keeps sessions to
	// their owner even for `owner`.
	mux.Handle("GET /api/v1/me/sessions", guard(http.HandlerFunc(auth.ListSessions)))
	mux.Handle("DELETE /api/v1/me/sessions", guard(http.HandlerFunc(auth.RevokeOtherSessions)))
	mux.Handle("DELETE /api/v1/me/sessions/{id}", guard(http.HandlerFunc(auth.RevokeSession)))

	// The limit sits **inside** the guard, and the order is the point: it is
	// keyed by the calling account, and the account only exists in the context
	// once the guard has resolved it. Outside, the key would be empty on every
	// request and the limit would count nothing while looking installed.
	//
	// It is httpx.RateLimit rather than the login guard because this endpoint
	// verifies no secret: what is worth counting here is every request, not the
	// failures (ADR-0005 §131, ADR-0010).
	mux.Handle("POST /api/v1/invitations", guard(inviteLimit(http.HandlerFunc(invitations.Create))))

	// Deliberately **not** behind the guard: whoever follows an invitation link
	// has no account yet, which is the point of the endpoint. What stands in
	// for a session is the token in the link, and it is single-use. The limit
	// is keyed by address rather than by account for the same reason.
	mux.Handle("POST /api/v1/invitations/accept", acceptLimit(http.HandlerFunc(invitations.Accept)))

	// Deliberately **not** behind the guard: somebody who cannot sign in is
	// exactly who this is for.
	//
	// Two limits apply and only one of them is here. ADR-0010 bounds this at
	// three an hour per account and ten an hour per address; the address one is
	// this middleware, and the account one lives inside the service, because the
	// address it is keyed by arrives in the request body and a middleware that
	// read it would consume it.
	//
	// Both fail closed. An unauthenticated endpoint that causes mail to land in
	// somebody else's inbox is the shape docs/nfr.md keeps fail-closed for.
	mux.Handle("POST /api/v1/auth/password/reset", resetRequestLimit(http.HandlerFunc(resets.Request)))
}

// RequireSession resolves the session cookie and refuses the request when
// there is no live session behind it.
//
// Everything it refuses arrives as the same 401 with the same body. The
// service has already decided what the log should say about which case it was.
func RequireSession(sessions *identitysvc.Sessions, log *slog.Logger) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(identityhttp.SessionCookieName)
			if err != nil {
				refuse(w, r)

				return
			}

			who, err := sessions.Authenticate(r.Context(), cookie.Value)
			if err != nil {
				if errors.Is(err, identitysvc.ErrUnauthenticated) {
					refuse(w, r)

					return
				}

				// A database that will not answer is not an unauthenticated
				// caller. Answering 401 would tell people to sign in again,
				// which they cannot, and hide the outage behind a login page.
				httpx.WriteInternalError(w, r, log, err)

				return
			}

			next.ServeHTTP(w, r.WithContext(identityhttp.WithCaller(r.Context(), who)))
		})
	}
}

func refuse(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, httpx.CodeUnauthenticated, "Silakan masuk lebih dulu.")
}
