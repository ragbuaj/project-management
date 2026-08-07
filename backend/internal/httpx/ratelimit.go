package httpx

import (
	"context"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"
)

// Limiter counts one hit against key and reports whether it is under the limit.
//
// It is declared here, where it is used, rather than beside the implementation
// (ADR-0008). The signature returns primitives for the same reason: a shared
// decision type would have to live in one package and be imported by the other,
// which is the coupling this arrangement exists to avoid.
type Limiter interface {
	Allow(ctx context.Context, key string) (allowed bool, retryAfter time.Duration, err error)
}

// OnLimiterFailure says what to do when the limiter itself fails — which in
// practice means Redis is unreachable.
//
// docs/nfr.md answers this per path rather than globally: the authentication
// endpoints fail closed, because turning off the brute-force guard is worse
// than refusing logins for a while, and everything else keeps serving.
type OnLimiterFailure int

const (
	// FailClosed refuses the request. It is the zero value on purpose: a call
	// site that forgets to choose gets the safe half.
	FailClosed OnLimiterFailure = iota
	FailOpen
)

// RateLimit rejects requests once key has been seen too often.
//
// key decides what is being limited — an account, an address, a session. It
// returns "" to skip the limit for a request, which is how a route exempts
// something it has already decided is safe.
func RateLimit(limiter Limiter, key func(*http.Request) string, onFailure OnLimiterFailure, log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			k := key(r)
			if k == "" {
				next.ServeHTTP(w, r)

				return
			}

			allowed, retryAfter, err := limiter.Allow(r.Context(), k)
			if err != nil {
				// The key is not logged. It is an account id or an address,
				// and this line is written on every request while Redis is
				// down.
				log.ErrorContext(r.Context(), "rate limiter unavailable",
					slog.String("request_id", RequestIDFrom(r.Context())),
					slog.String("path", r.URL.Path),
					slog.Bool("fail_open", onFailure == FailOpen),
					slog.String("error", err.Error()),
				)

				if onFailure == FailOpen {
					next.ServeHTTP(w, r)

					return
				}

				WriteRateLimited(w, r, retryAfterUnavailable)

				return
			}

			if !allowed {
				WriteRateLimited(w, r, retryAfter)

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// retryAfterUnavailable is what a failed-closed request is told to wait. The
// limiter could not say, so this is a guess — short enough that a client
// retries rather than gives up, long enough not to become a tight loop against
// a Redis that is already struggling.
const retryAfterUnavailable = 30 * time.Second

// WriteRateLimited answers 429 with the Retry-After the contract declares.
//
// It is exported because the login endpoint refuses from inside its handler
// rather than from this middleware: ADR-0010 counts failed attempts, and
// whether an attempt failed is only known after the password has been checked.
// That endpoint still has to produce the same status, the same body, and the
// same rounding as everything the middleware refuses, and the way to guarantee
// that is for both to call one function.
func WriteRateLimited(w http.ResponseWriter, r *http.Request, retryAfter time.Duration) {
	// Retry-After is whole seconds (docs/api/openapi.yaml). Rounding up rather
	// than down, so a client that obeys it exactly is not turned away again for
	// arriving a fraction of a second early.
	seconds := max(int64(math.Ceil(retryAfter.Seconds())), 1)

	w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))

	WriteError(w, r, CodeRateLimited, "Terlalu banyak percobaan. Coba lagi sebentar.")
}
