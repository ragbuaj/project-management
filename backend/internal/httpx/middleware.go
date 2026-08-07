package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// Middleware wraps a handler. Chain applies them.
type Middleware func(http.Handler) http.Handler

// Chain wraps h so that mw[0] is the outermost layer and therefore the first
// to see a request and the last to see the response.
//
// Order matters and is not interchangeable: RequestID has to run before
// anything that logs, and Recover has to sit inside RequestID so its response
// carries an id, but outside the request log so a panic is still recorded as a
// finished request.
func Chain(h http.Handler, mw ...Middleware) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}

	return h
}

// recorder remembers what the handler answered, because net/http offers no way
// to ask afterwards.
type recorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *recorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	// A handler that writes without calling WriteHeader has answered 200.
	if r.status == 0 {
		r.status = http.StatusOK
	}

	n, err := r.ResponseWriter.Write(b)
	r.bytes += n

	return n, err
}

// Unwrap lets http.ResponseController reach the real writer, so Flush and
// Hijack keep working through this wrapper. Without it, streaming and the
// websocket upgrade in Phase 8 would both break here, and the failure would
// look like a bug in whatever tried to use them.
func (r *recorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// LogRequests writes one line per finished request.
//
// The query string is deliberately left out. A path carries ids, which
// docs/data-model.md permits in logs; a query carries search terms, filter
// values, and whatever else a client puts there, which it does not.
func LogRequests(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			rec := &recorder{ResponseWriter: w}

			next.ServeHTTP(rec, r)

			if rec.status == 0 {
				rec.status = http.StatusOK
			}

			level := slog.LevelInfo
			if rec.status >= http.StatusInternalServerError {
				level = slog.LevelError
			}

			log.LogAttrs(r.Context(), level, "request",
				slog.String("request_id", RequestIDFrom(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int("bytes", rec.bytes),
				slog.Duration("duration", time.Since(started)),
			)
		})
	}
}

// Recover turns a panic into a 500 instead of a dropped connection.
//
// The stack goes to the log and never to the caller: it names file paths,
// package versions, and sometimes the values that were being handled.
func Recover(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &recorder{ResponseWriter: w}

			// recoverPanic is the deferred call itself rather than a closure
			// around one, because recover() only reports a panic when called
			// directly from the function that defer names.
			defer recoverPanic(r.Context(), log, rec, r)

			next.ServeHTTP(rec, r)
		})
	}
}

func recoverPanic(ctx context.Context, log *slog.Logger, rec *recorder, r *http.Request) {
	recovered := recover()
	if recovered == nil {
		return
	}

	// net/http panics with this on purpose to abort a response it has decided
	// to give up on. Swallowing it would leave the connection in a state the
	// server expects to have torn down.
	if recovered == http.ErrAbortHandler { //nolint:errorlint // sentinel is panicked as a value, not wrapped
		panic(recovered)
	}

	log.ErrorContext(ctx, "panic serving request",
		slog.String("request_id", RequestIDFrom(ctx)),
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Any("panic", recovered),
		slog.String("stack", string(debug.Stack())),
	)

	// Once a status is out, the response is committed and a second WriteHeader
	// would be ignored with a warning. The log line above is the only report
	// available in that case.
	if rec.status != 0 {
		return
	}

	WriteError(rec, r, CodeInternal,
		"Terjadi kesalahan di server. Sertakan request_id kalau melaporkannya.")
}
