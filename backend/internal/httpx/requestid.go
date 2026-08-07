package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// RequestIDHeader carries the id back to the caller so a bug report can quote
// it and the server log can be searched for the same string.
const RequestIDHeader = "X-Request-Id"

type contextKey int

const requestIDKey contextKey = iota

// RequestID gives every request an id and puts it in the context and the
// response header.
//
// The id is always generated here. An inbound X-Request-Id is deliberately
// ignored: it is caller-controlled text that would otherwise land in every log
// line for the request, where it can forge fields, break the parser, or be
// reused across unrelated requests to make them look like one. Nothing in
// front of this server sets the header today, so there is no trace to join.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()

		w.Header().Set(RequestIDHeader, id)

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

// RequestIDFrom returns the id RequestID assigned, or "" when the middleware
// did not run — which is the case in a test that calls a handler directly.
//
// Callers must treat "" as "unknown" rather than as an error. An error
// response with no id is still worth sending.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)

	return id
}

// newRequestID returns 128 random bits as hex.
//
// Random rather than sequential: an id that counts tells every caller how much
// traffic the system takes. crypto/rand.Read never returns an error as of Go
// 1.24 — it panics instead — so there is no failure path to handle here.
func newRequestID() string {
	var b [16]byte

	_, _ = rand.Read(b[:])

	return hex.EncodeToString(b[:])
}
