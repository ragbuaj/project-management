package httpx

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
)

// CSRFCookieName holds one half of the double-submit pair ADR-0005 requires.
//
// The __Host- prefix is load-bearing here rather than merely careful. A
// double-submit token is a secret only for as long as nobody but this server
// can write the cookie, and a sibling subdomain writing a plain `csrf` cookie
// would hand an attacker both halves at once. A __Host- cookie is host-only by
// definition — a browser refuses one that names a Domain — so a sibling
// cannot set it.
//
// The cookie itself is not HttpOnly, because the client has to read it to send
// the header. Issuing it is the other half of this step and lands next.
const CSRFCookieName = "__Host-csrf"

// CSRFHeaderName is the other half. A header is what makes this work at all:
// an HTML form on another site can send a cross-site POST carrying cookies,
// but it cannot set this header, and a script that tries needs a preflight
// this server never grants.
const CSRFHeaderName = "X-CSRF-Token"

// CSRF refuses a request that changes something unless it presents the pair.
//
// ADR-0005 asks for this at the router rather than per handler, so that
// forgetting it is not an option a future handler has.
//
// A caller presenting Authorization: Bearer is exempt (ADR-0005). That token
// is not an ambient credential — a browser never attaches it on its own, so
// there is nothing for another site to ride on.
func CSRF(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if csrfExempt(r) {
				next.ServeHTTP(w, r)

				return
			}

			if reason := csrfFailure(csrfCookieToken(r), r.Header.Get(CSRFHeaderName)); reason != "" {
				// ADR-0005 asks for this to be watched: in production the count
				// should be zero, and anything above it is either a
				// misconfigured client or somebody trying. Neither token half
				// is logged — the value that failed to match is as sensitive as
				// the one that would have.
				log.WarnContext(r.Context(), "csrf check failed",
					slog.String("request_id", RequestIDFrom(r.Context())),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("reason", reason),
				)

				WriteError(w, r, CodeForbidden,
					"Permintaan ditolak karena token CSRF tidak cocok. Muat ulang halaman lalu coba lagi.")

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// csrfExempt reports whether the request may proceed without the pair.
//
// The safe methods are an allowlist rather than the unsafe ones being a
// blocklist (rules/40-security.md). The difference shows up the day something
// starts serving a method nobody listed: with an allowlist it is protected by
// default, with a blocklist it is not.
func csrfExempt(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		// Prefix match, case-insensitive, because the scheme name in an
		// Authorization header is not case sensitive (RFC 9110 §11.1). Only
		// Bearer is exempt: Basic credentials are as ambient as a cookie.
		return strings.HasPrefix(strings.ToLower(r.Header.Get("Authorization")), "bearer ")
	}
}

func csrfCookieToken(r *http.Request) string {
	cookie, err := r.Cookie(CSRFCookieName)
	if err != nil {
		return ""
	}

	return cookie.Value
}

// csrfFailure names why the pair was refused, or returns "" when it holds.
//
// The reason is for the log only. All three reach the caller as the same 403:
// which half was wrong is not something the caller needs to be told, and the
// difference is only ever useful to somebody probing.
func csrfFailure(cookieToken, headerToken string) string {
	switch {
	case cookieToken == "":
		return "no csrf cookie"
	case headerToken == "":
		return "no csrf header"
	case subtle.ConstantTimeCompare([]byte(cookieToken), []byte(headerToken)) != 1:
		return "csrf header does not match the cookie"
	default:
		return ""
	}
}
