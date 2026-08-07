package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// ErrorCode is the stable half of an error response. docs/api/openapi.yaml
// declares these and clients are allowed to branch on them, so the set only
// ever grows and a member is never renamed.
type ErrorCode string

const (
	CodeValidationFailed ErrorCode = "VALIDATION_FAILED"
	CodeUnauthenticated  ErrorCode = "UNAUTHENTICATED"
	CodeForbidden        ErrorCode = "FORBIDDEN"
	CodeNotFound         ErrorCode = "NOT_FOUND"
	CodeConflict         ErrorCode = "CONFLICT"
	CodeRateLimited      ErrorCode = "RATE_LIMITED"
	CodeInternal         ErrorCode = "INTERNAL"
)

// statusOf maps each code to its status. The mapping lives here rather than at
// each call site so a handler cannot answer 200 with an error body, or 500
// with NOT_FOUND — both of which are easy to write and hard to notice.
//
// docs/authorization.md requires 404 for a resource the caller may not see, so
// FORBIDDEN is only for the case where the caller may see the resource but not
// perform the action.
func statusOf(code ErrorCode) int {
	switch code {
	case CodeValidationFailed:
		return http.StatusBadRequest
	case CodeUnauthenticated:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeConflict:
		return http.StatusConflict
	case CodeRateLimited:
		return http.StatusTooManyRequests
	case CodeInternal:
		return http.StatusInternalServerError
	default:
		// An unknown code is a programming error, and answering 200 would let
		// it reach production unnoticed.
		return http.StatusInternalServerError
	}
}

// FieldError names one field that failed validation. Both halves are stable
// enough for a client to map to its own wording.
type FieldError struct {
	Field string `json:"field"`
	Code  string `json:"code"`
}

type errorBody struct {
	Code      ErrorCode    `json:"code"`
	Message   string       `json:"message"`
	Details   []FieldError `json:"details,omitempty"`
	RequestID string       `json:"request_id"`
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

// WriteError sends the error shape docs/api/openapi.yaml declares.
//
// message is shown to a person, so it must never carry a stack trace, a SQL
// query, a library version, or a file path. Whatever detail explains the
// failure goes to the server log against the same request id, which is what
// the caller quotes when reporting it.
func WriteError(w http.ResponseWriter, r *http.Request, code ErrorCode, message string, details ...FieldError) {
	body := errorEnvelope{Error: errorBody{
		Code:      code,
		Message:   message,
		Details:   details,
		RequestID: RequestIDFrom(r.Context()),
	}}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusOf(code))

	// The status and headers are already sent, so a failure here cannot be
	// turned into a different response. Nothing to do but drop it; the request
	// log records the status that was written either way.
	_ = json.NewEncoder(w).Encode(body)
}

// WriteInternalError logs cause and answers with a body that says nothing
// about it.
//
// The split is the point. A handler that hits an unexpected error should have
// exactly one obvious way to report it, and that way must make leaking the
// detail the harder option rather than the default one.
func WriteInternalError(w http.ResponseWriter, r *http.Request, log *slog.Logger, cause error) {
	log.ErrorContext(r.Context(), "request failed",
		slog.String("request_id", RequestIDFrom(r.Context())),
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.String("error", cause.Error()),
	)

	WriteError(w, r, CodeInternal, "Terjadi kesalahan di server. Sertakan request_id kalau melaporkannya.")
}
