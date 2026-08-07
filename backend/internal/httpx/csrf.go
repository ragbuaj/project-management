package httpx

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
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
// It is deliberately not HttpOnly. The SPA has to read it to echo it back in
// the header, and that echo is the entire mechanism.
const CSRFCookieName = "__Host-csrf"

// CSRFHeaderName is the other half. A header is what makes this work at all:
// an HTML form on another site can send a cross-site POST carrying cookies,
// but it cannot set this header, and a script that tries needs a preflight
// this server never grants.
const CSRFHeaderName = "X-CSRF-Token"

// csrfTokenBytes matches the session token in ADR-0005. The value is compared
// against itself rather than looked up, so nothing here depends on its width —
// but a token narrow enough to guess would make the pair guessable, and 256
// bits is not.
const csrfTokenBytes = 32

// CSRF refuses a request that changes something unless it presents the pair,
// and hands a token to callers that do not have one yet.
//
// Both halves live here on purpose. ADR-0005 asks for the check at the router
// rather than per handler, so that forgetting it is not an option a future
// handler has; issuing the cookie anywhere else would reopen exactly that hole
// from the other side, because a mutating endpoint would then depend on some
// earlier endpoint having remembered to hand out a token.
//
// A caller presenting Authorization: Bearer is exempt (ADR-0005). That token
// is not an ambient credential — a browser never attaches it on its own, so
// there is nothing for another site to ride on.
func CSRF(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			presented, ok := csrfCookieToken(r)
			if !ok {
				// Either the caller has never been here, or it holds a value
				// this server did not issue. Replacing it is what keeps the
				// second case from being a browser that can never POST again.
				//
				// Written before anything else touches the response, because a
				// status that is already out cannot grow headers afterwards.
				if err := setCSRFCookie(w); err != nil {
					WriteInternalError(w, r, log, err)

					return
				}
			}

			if csrfExempt(r) {
				next.ServeHTTP(w, r)

				return
			}

			// A token minted a moment ago was not presented with this request,
			// so presented is still empty and the check below refuses. That is
			// the intent: the first request a client makes must not be one that
			// changes data.
			if reason := csrfFailure(presented, r.Header.Get(CSRFHeaderName)); reason != "" {
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

// csrfCookieToken returns the token the request presents, and whether it is
// one this server could have issued.
//
// The shape is checked so that a malformed value is replaced rather than
// carried around. It is not a security check: the pair is compared against
// itself, so a well-formed value nobody issued would still match a header
// echoing it. What stops that from being an attack is the __Host- prefix,
// which is why the cookie carries it.
func csrfCookieToken(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(CSRFCookieName)
	if err != nil {
		return "", false
	}

	raw, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil || len(raw) != csrfTokenBytes {
		return "", false
	}

	return cookie.Value, true
}

// setCSRFCookie mints a token and writes it.
//
// No Expires and no Max-Age, so it lives as long as the browser session —
// shorter than the 14-day session cookie on purpose. Losing it costs one safe
// request to get another, and every way into this application starts with one:
// the SPA is loaded with a GET.
func setCSRFCookie(w http.ResponseWriter) error {
	raw := make([]byte, csrfTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("read csrf token: %w", err)
	}

	// gosec G124 asks for HttpOnly on every cookie, and this is the one place
	// it cannot be given: a cookie the SPA cannot read is a header the SPA can
	// never send. The other two attributes it looks for are set, and the
	// __Host- prefix covers what HttpOnly would not have anyway.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: this cookie exists to be read by the client
		Name:  CSRFCookieName,
		Value: base64.RawURLEncoding.EncodeToString(raw),
		Path:  "/",
		// Stated rather than left to the zero value, so it reads as decided
		// rather than forgotten.
		HttpOnly: false,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	return nil
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
