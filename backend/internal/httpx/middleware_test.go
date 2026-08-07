package httpx_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/ragbuaj/project-management/backend/internal/httpx"
)

// A panic must not drop the connection, and its stack must not travel to the
// caller — it names file paths, package versions, and sometimes the values
// being handled.
func TestAPanicBecomesA500AndTheStackStaysInTheLog(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	h := httpx.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("the assignee was nil")
		}),
		httpx.RequestID,
		httpx.Recover(capturingLogger(&buf)),
	)

	rec := get(t, h, "/")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}

	if strings.Contains(rec.Body.String(), "assignee") || strings.Contains(rec.Body.String(), "middleware_test.go") {
		t.Errorf("the response leaks the panic: %s", rec.Body.String())
	}

	if !strings.Contains(buf.String(), "the assignee was nil") {
		t.Error("the panic value was not logged")
	}

	if !strings.Contains(buf.String(), "middleware_test.go") {
		t.Error("the stack was not logged, so nobody can find where it happened")
	}

	body := decodeError(t, rec.Body.Bytes())
	if body.Error.RequestID == "" {
		t.Error("the panic response carries no request id")
	}
}

// Once a status is out the response is committed, and a second WriteHeader is
// ignored with a warning. Recover must notice and leave it alone rather than
// corrupting a response that is already on its way.
func TestAPanicAfterTheResponseStartedDoesNotRewriteIt(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	h := httpx.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"1"}`))
			panic("too late")
		}),
		httpx.RequestID,
		httpx.Recover(capturingLogger(&buf)),
	)

	rec := get(t, h, "/")

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201 — the committed response was rewritten", rec.Code)
	}

	if !strings.Contains(buf.String(), "too late") {
		t.Error("the panic was swallowed without a log line")
	}
}

// net/http panics with ErrAbortHandler on purpose to abandon a response.
// Swallowing it leaves the connection in a state the server expected to tear
// down.
func TestErrAbortHandlerIsNotSwallowed(t *testing.T) {
	t.Parallel()

	h := httpx.Recover(discardLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	defer func() {
		if recovered := recover(); recovered != http.ErrAbortHandler { //nolint:errorlint // panicked as a value
			t.Errorf("recovered %v, want http.ErrAbortHandler to travel on", recovered)
		}
	}()

	get(t, h, "/")
}

// A query string carries search terms and filter values — things
// docs/data-model.md keeps out of logs. A path carries ids, which it permits.
func TestTheRequestLogRecordsThePathButNotTheQuery(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	h := httpx.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
		httpx.RequestID,
		httpx.LogRequests(capturingLogger(&buf)),
	)

	get(t, h, "/cards/019f-abc?q=gaji+direktur&assignee=me")

	logged := buf.String()

	if strings.Contains(logged, "gaji") || strings.Contains(logged, "assignee=me") {
		t.Errorf("the query string reached the log: %s", logged)
	}

	if !strings.Contains(logged, "/cards/019f-abc") {
		t.Errorf("the path is missing from the log: %s", logged)
	}

	var line struct {
		Level     string `json:"level"`
		Status    int    `json:"status"`
		Method    string `json:"method"`
		RequestID string `json:"request_id"`
	}

	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("decode log line: %v", err)
	}

	if line.Status != http.StatusNoContent {
		t.Errorf("status logged as %d, want 204", line.Status)
	}

	if line.Method != http.MethodGet {
		t.Errorf("method logged as %q", line.Method)
	}

	if line.RequestID == "" {
		t.Error("the log line carries no request id, so it cannot be joined to anything")
	}
}

// A handler that writes a body without calling WriteHeader has answered 200,
// and one that answers nothing at all has too. Logging 0 in either case would
// make every dashboard built on this field wrong.
func TestAnImplicit200IsLoggedAs200(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"writes a body", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }},
		{"writes nothing", func(w http.ResponseWriter, r *http.Request) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			get(t, httpx.LogRequests(capturingLogger(&buf))(tc.handler), "/")

			var line struct {
				Status int `json:"status"`
			}

			if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
				t.Fatalf("decode log line: %v", err)
			}

			if line.Status != http.StatusOK {
				t.Errorf("status logged as %d, want 200", line.Status)
			}
		})
	}
}

// A 500 that logs at info sits below the level most deployments actually watch.
func TestServerErrorsAreLoggedAtErrorLevel(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	h := httpx.LogRequests(capturingLogger(&buf))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))

	get(t, h, "/")

	var line struct {
		Level string `json:"level"`
	}

	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("decode log line: %v", err)
	}

	if line.Level != slog.LevelError.String() {
		t.Errorf("level = %q, want %q", line.Level, slog.LevelError)
	}
}

// http.ResponseController reaches the real writer through Unwrap. Without it,
// streaming and the websocket upgrade in Phase 8 both break here, and the
// failure looks like a bug in whatever tried to use them.
func TestTheRecorderDoesNotHideFlush(t *testing.T) {
	t.Parallel()

	var flushed bool

	h := httpx.LogRequests(discardLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("Flush(): %v", err)

			return
		}

		flushed = true
	}))

	get(t, h, "/")

	if !flushed {
		t.Error("Flush did not reach the underlying writer")
	}
}

// The order cmd/api uses, held here so it cannot drift.
//
// A panic unwinds through every frame above it, so Recover placed outside
// LogRequests would take the panic before LogRequests could write its line —
// and the request would be missing from the log it is meant to appear in.
// Inside, Recover turns the panic into a 500, returns normally, and the
// request is logged as what it became.
func TestAPanickingRequestStillAppearsInTheRequestLog(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	log := capturingLogger(&buf)

	h := httpx.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("boom")
		}),
		httpx.RequestID,
		httpx.LogRequests(log),
		httpx.Recover(log),
	)

	if rec := get(t, h, "/cards"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}

	var requestLine bool

	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		var entry struct {
			Msg    string `json:"msg"`
			Status int    `json:"status"`
		}

		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}

		if entry.Msg != "request" {
			continue
		}

		requestLine = true

		if entry.Status != http.StatusInternalServerError {
			t.Errorf("the request was logged with status %d, want 500", entry.Status)
		}
	}

	if !requestLine {
		t.Error("a panicking request produced no request log line")
	}
}

// Chain has to apply the outermost middleware first, or Recover would sit
// outside RequestID and its response would carry no id.
func TestChainAppliesTheFirstMiddlewareOutermost(t *testing.T) {
	t.Parallel()

	var order []string

	mark := func(name string) httpx.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}

	get(t, httpx.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { order = append(order, "handler") }),
		mark("first"), mark("second"),
	), "/")

	want := []string{"first", "second", "handler"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", order, want)
	}
}
