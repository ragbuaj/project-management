package httpx_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ragbuaj/project-management/backend/internal/httpx"
)

// capturingLogger returns a logger writing JSON into buf, so a test can assert
// on what was logged rather than only on what was answered.
func capturingLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil))

	return rec
}

// errorEnvelope mirrors the shape docs/api/openapi.yaml declares. It is
// spelled out here rather than reusing the production type on purpose: a test
// that decodes with the same struct it encoded with would still pass if both
// drifted away from the contract together.
type errorEnvelope struct {
	Error struct {
		Code      string             `json:"code"`
		Message   string             `json:"message"`
		Details   []httpx.FieldError `json:"details"`
		RequestID string             `json:"request_id"`
	} `json:"error"`
}

func decodeError(t *testing.T, body []byte) errorEnvelope {
	t.Helper()

	var out errorEnvelope

	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode error body %q: %v", body, err)
	}

	return out
}

// An id that a caller supplies is caller-controlled text that would otherwise
// appear in every log line for the request. Accepting it lets someone forge
// fields, or make unrelated requests look like one.
func TestRequestIDIsGeneratedAndNeverTakenFromTheCaller(t *testing.T) {
	t.Parallel()

	var seen string

	h := httpx.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = httpx.RequestIDFrom(r.Context())
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Header.Set(httpx.RequestIDHeader, "forged-by-the-caller")

	h.ServeHTTP(rec, req)

	if seen == "forged-by-the-caller" {
		t.Fatal("the caller's request id was trusted")
	}

	if len(seen) != 32 {
		t.Errorf("request id %q is %d characters, want 32 hex characters", seen, len(seen))
	}

	if got := rec.Header().Get(httpx.RequestIDHeader); got != seen {
		t.Errorf("response header carries %q, context carries %q", got, seen)
	}
}

// Two requests sharing an id would make the log unusable for the one thing it
// is for: following a single request through it.
func TestEachRequestGetsItsOwnID(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool)

	h := httpx.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[httpx.RequestIDFrom(r.Context())] = true
	}))

	const requests = 50

	for range requests {
		get(t, h, "/")
	}

	if len(seen) != requests {
		t.Errorf("%d requests produced %d distinct ids", requests, len(seen))
	}
}

// A handler called directly, as in a unit test, has no id. That must degrade to
// "unknown" rather than to a panic — an error response without an id is still
// worth sending.
func TestRequestIDFromIsEmptyWithoutTheMiddleware(t *testing.T) {
	t.Parallel()

	if id := httpx.RequestIDFrom(t.Context()); id != "" {
		t.Errorf("RequestIDFrom() = %q, want empty", id)
	}
}

// The status belongs to the code, not to the call site. A handler answering 200
// with an error body, or 500 with NOT_FOUND, is easy to write and hard to spot.
func TestEachErrorCodeCarriesItsOwnStatus(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		code   httpx.ErrorCode
		status int
	}{
		{httpx.CodeValidationFailed, http.StatusBadRequest},
		{httpx.CodeUnauthenticated, http.StatusUnauthorized},
		{httpx.CodeForbidden, http.StatusForbidden},
		{httpx.CodeNotFound, http.StatusNotFound},
		{httpx.CodeConflict, http.StatusConflict},
		{httpx.CodeRateLimited, http.StatusTooManyRequests},
		{httpx.CodeInternal, http.StatusInternalServerError},
	} {
		t.Run(string(tc.code), func(t *testing.T) {
			t.Parallel()

			h := httpx.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				httpx.WriteError(w, r, tc.code, "pesan")
			}))

			rec := get(t, h, "/")

			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}

			body := decodeError(t, rec.Body.Bytes())

			if body.Error.Code != string(tc.code) {
				t.Errorf("code = %q, want %q", body.Error.Code, tc.code)
			}

			if body.Error.RequestID == "" {
				t.Error("the error body carries no request_id, so nobody can report it")
			}

			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
		})
	}
}

// An unknown code means a caller made one up. Answering 200 would let that
// reach production as a success.
func TestAnUnknownErrorCodeIsNotASuccess(t *testing.T) {
	t.Parallel()

	h := httpx.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, httpx.ErrorCode("MADE_UP"), "pesan")
	}))

	if rec := get(t, h, "/"); rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestValidationDetailsSurviveTheEnvelope(t *testing.T) {
	t.Parallel()

	h := httpx.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, httpx.CodeValidationFailed, "Input tidak valid",
			httpx.FieldError{Field: "email", Code: "invalid_format"},
			httpx.FieldError{Field: "name", Code: "required"},
		)
	}))

	body := decodeError(t, get(t, h, "/").Body.Bytes())

	if len(body.Error.Details) != 2 {
		t.Fatalf("details has %d entries, want 2", len(body.Error.Details))
	}

	if body.Error.Details[0].Field != "email" || body.Error.Details[0].Code != "invalid_format" {
		t.Errorf("first detail = %+v", body.Error.Details[0])
	}
}

// The whole reason WriteInternalError exists: the cause is for the log, and the
// caller gets a request id to quote. A stack trace, a SQL query, or a file path
// reaching the client is an information leak, not a helpful error message.
func TestAnInternalErrorTellsTheLogAndNotTheCaller(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	const secret = "pq: relation \"users\" does not exist at /srv/app/repo.go:42"

	h := httpx.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteInternalError(w, r, capturingLogger(&buf), errStub(secret))
	}))

	rec := get(t, h, "/")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}

	if strings.Contains(rec.Body.String(), "users") || strings.Contains(rec.Body.String(), "repo.go") {
		t.Errorf("the response leaks the cause: %s", rec.Body.String())
	}

	if !strings.Contains(buf.String(), "repo.go") {
		t.Errorf("the log does not carry the cause: %s", buf.String())
	}

	body := decodeError(t, rec.Body.Bytes())
	if !strings.Contains(buf.String(), body.Error.RequestID) {
		t.Error("the log and the response do not share a request id, so the two cannot be joined")
	}
}

// errStub carries a message without pulling in errors.New at every call site.
type errStub string

func (e errStub) Error() string { return string(e) }
