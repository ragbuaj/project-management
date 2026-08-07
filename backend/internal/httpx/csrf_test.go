package httpx_test

import (
	"bytes"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ragbuaj/project-management/backend/internal/httpx"
)

// aToken is shaped like the value this server issues: 32 bytes, base64url, no
// padding. Spelled out rather than borrowed from the production code so a
// change to the shape shows up here as a failure rather than as agreement.
var aToken = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x2a}, 32))

// csrfCall is one request against the middleware. An empty field is absent
// rather than empty, which is the distinction most of these tests turn on.
type csrfCall struct {
	method string
	cookie string
	header string
	bearer string
}

func (c csrfCall) do(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), c.method, "/api/v1/cards", nil)

	if c.cookie != "" {
		req.AddCookie(&http.Cookie{Name: httpx.CSRFCookieName, Value: c.cookie})
	}

	if c.header != "" {
		req.Header.Set(httpx.CSRFHeaderName, c.header)
	}

	if c.bearer != "" {
		req.Header.Set("Authorization", c.bearer)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// guardedBy wraps a handler that records whether it was reached. RequestID is
// included because a refusal carries one, same as every other response here.
func guardedBy(t *testing.T, log *slog.Logger) (http.Handler, *bool) {
	t.Helper()

	reached := new(bool)

	h := httpx.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*reached = true
			w.WriteHeader(http.StatusNoContent)
		}),
		httpx.RequestID,
		httpx.CSRF(log),
	)

	return h, reached
}

// issuedCSRFCookie returns the cookie the response sets, or nil if it sets
// none. Set-Cookie is parsed rather than read off ResponseRecorder.Result(),
// which hands back a body nobody here wants to own.
func issuedCSRFCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()

	for _, line := range rec.Header().Values("Set-Cookie") {
		cookie, err := http.ParseSetCookie(line)
		if err != nil {
			t.Fatalf("parse Set-Cookie %q: %v", line, err)
		}

		if cookie.Name == httpx.CSRFCookieName {
			return cookie
		}
	}

	return nil
}

// The whole point of the middleware: a cross-site form can make a browser send
// its cookies, but it cannot make it send this header.
func TestAMutatingRequestWithoutTheHeaderIsRefused(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			h, reached := guardedBy(t, discardLogger())

			rec := csrfCall{method: method, cookie: aToken}.do(t, h)

			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", rec.Code)
			}

			if *reached {
				t.Error("the handler ran on a request with no csrf header")
			}

			body := decodeError(t, rec.Body.Bytes())
			if body.Error.Code != string(httpx.CodeForbidden) {
				t.Errorf("code = %q, want FORBIDDEN", body.Error.Code)
			}

			if body.Error.RequestID == "" {
				t.Error("the refusal carries no request id")
			}
		})
	}
}

// A header alone proves nothing: whoever could set it could also invent the
// value. It is the match with a cookie the sender cannot read that carries the
// proof.
func TestAMutatingRequestWithoutTheCookieIsRefused(t *testing.T) {
	t.Parallel()

	h, reached := guardedBy(t, discardLogger())

	rec := csrfCall{method: http.MethodPost, header: aToken}.do(t, h)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}

	if *reached {
		t.Error("the handler ran on a request with no csrf cookie")
	}
}

func TestAMismatchedPairIsRefused(t *testing.T) {
	t.Parallel()

	other := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x2b}, 32))

	h, reached := guardedBy(t, discardLogger())

	rec := csrfCall{method: http.MethodPost, cookie: aToken, header: other}.do(t, h)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}

	if *reached {
		t.Error("the handler ran on a mismatched pair")
	}
}

// A prefix that compares equal for its whole length must not pass. The compare
// is length-sensitive, and this is the case that proves it.
func TestATruncatedTokenIsRefused(t *testing.T) {
	t.Parallel()

	h, reached := guardedBy(t, discardLogger())

	rec := csrfCall{method: http.MethodPost, cookie: aToken, header: aToken[:len(aToken)-1]}.do(t, h)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}

	if *reached {
		t.Error("a truncated token was accepted")
	}
}

func TestAMatchingPairIsLetThrough(t *testing.T) {
	t.Parallel()

	h, reached := guardedBy(t, discardLogger())

	rec := csrfCall{method: http.MethodPost, cookie: aToken, header: aToken}.do(t, h)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204: %s", rec.Code, rec.Body)
	}

	if !*reached {
		t.Error("a matching pair did not reach the handler")
	}
}

// Reading is not changing. A GET that had to carry a token would mean every
// link into the application needed one, which is not something a link can do.
//
// A safe request is also where the token comes from, so each of these has to
// leave the caller able to make an unsafe one afterwards.
func TestSafeMethodsAreServedWithoutThePairAndIssueTheCookie(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			h, reached := guardedBy(t, discardLogger())

			rec := csrfCall{method: method}.do(t, h)

			if rec.Code != http.StatusNoContent {
				t.Errorf("status = %d, want 204", rec.Code)
			}

			if !*reached {
				t.Error("a safe method was refused")
			}

			cookie := issuedCSRFCookie(t, rec)
			if cookie == nil {
				t.Fatal("no csrf cookie was issued, so nothing can ever POST")
			}

			if raw, err := base64.RawURLEncoding.DecodeString(cookie.Value); err != nil || len(raw) != 32 {
				t.Errorf("issued token is not 32 base64url bytes: %q (err %v)", cookie.Value, err)
			}
		})
	}
}

// Attributes ADR-0005 asks for. HttpOnly is the one that must be off — the SPA
// reads this cookie — and Secure and the __Host- constraints are the ones that
// must hold, or the browser rejects the cookie outright.
func TestTheIssuedCookieCarriesTheAttributesTheSPANeeds(t *testing.T) {
	t.Parallel()

	h, _ := guardedBy(t, discardLogger())

	cookie := issuedCSRFCookie(t, csrfCall{method: http.MethodGet}.do(t, h))
	if cookie == nil {
		t.Fatal("no csrf cookie was issued")
	}

	if cookie.HttpOnly {
		t.Error("the cookie is HttpOnly, so the SPA cannot read it and can never send the header")
	}

	if !cookie.Secure {
		t.Error("the cookie is not Secure")
	}

	if cookie.Path != "/" {
		t.Errorf("path = %q, want / — a __Host- cookie is rejected otherwise", cookie.Path)
	}

	if cookie.Domain != "" {
		t.Errorf("domain = %q, want empty — a __Host- cookie may not name one", cookie.Domain)
	}

	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("samesite = %v, want Lax", cookie.SameSite)
	}
}

// Reissuing on every request would race the SPA: a page that read the cookie,
// then sent it, would be answered with a different one and fail its next call.
func TestAValidCookieIsLeftAlone(t *testing.T) {
	t.Parallel()

	h, _ := guardedBy(t, discardLogger())

	rec := csrfCall{method: http.MethodGet, cookie: aToken}.do(t, h)

	if cookie := issuedCSRFCookie(t, rec); cookie != nil {
		t.Errorf("a valid cookie was replaced with %q", cookie.Value)
	}
}

func TestEachClientGetsItsOwnToken(t *testing.T) {
	t.Parallel()

	h, _ := guardedBy(t, discardLogger())

	first := issuedCSRFCookie(t, csrfCall{method: http.MethodGet}.do(t, h))
	second := issuedCSRFCookie(t, csrfCall{method: http.MethodGet}.do(t, h))

	if first == nil || second == nil {
		t.Fatal("a request went without a cookie")
	}

	if first.Value == second.Value {
		t.Error("two clients were handed the same token")
	}
}

// A value this server did not issue must be replaced, not carried around. If
// it were kept, a client echoing it would keep matching it — the pair would
// hold while resting on a token of unknown origin.
func TestAMalformedCookieIsRefusedEvenWhenTheHeaderEchoesIt(t *testing.T) {
	t.Parallel()

	h, reached := guardedBy(t, discardLogger())

	rec := csrfCall{method: http.MethodPost, cookie: "not-a-token", header: "not-a-token"}.do(t, h)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 — a token nobody issued was accepted", rec.Code)
	}

	if *reached {
		t.Error("the handler ran on a self-supplied token")
	}

	replacement := issuedCSRFCookie(t, rec)
	if replacement == nil {
		t.Fatal("the malformed cookie was not replaced, so this client can never succeed")
	}

	if replacement.Value == "not-a-token" {
		t.Error("the replacement is the malformed value again")
	}
}

// ADR-0005 exempts Bearer callers: that token is not ambient, so a browser
// never attaches it on somebody else's behalf. The scheme name is not case
// sensitive (RFC 9110), and a client spelling it in lower case is still one.
func TestABearerCallerIsExemptInEitherCase(t *testing.T) {
	t.Parallel()

	for _, authorization := range []string{"Bearer pmt_abc", "bearer pmt_abc"} {
		t.Run(authorization, func(t *testing.T) {
			t.Parallel()

			h, reached := guardedBy(t, discardLogger())

			rec := csrfCall{method: http.MethodPost, bearer: authorization}.do(t, h)

			if rec.Code != http.StatusNoContent {
				t.Errorf("status = %d, want 204: %s", rec.Code, rec.Body)
			}

			if !*reached {
				t.Error("a Bearer caller was refused")
			}
		})
	}
}

// The exemption is for Bearer, not for any Authorization header. Basic
// credentials are as ambient as a cookie — a browser resends them on its own
// once it has them, which is exactly what this middleware exists to distrust.
func TestOtherAuthorizationSchemesAreNotExempt(t *testing.T) {
	t.Parallel()

	h, reached := guardedBy(t, discardLogger())

	rec := csrfCall{method: http.MethodPost, bearer: "Basic YWxpY2U6c2VjcmV0"}.do(t, h)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}

	if *reached {
		t.Error("a Basic caller skipped the csrf check")
	}
}

// ADR-0005 asks for failures to be watched, so they have to be findable. The
// tokens themselves must not be in the line.
func TestARefusalIsLoggedWithoutEitherToken(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	h, _ := guardedBy(t, capturingLogger(&buf))

	csrfCall{method: http.MethodPost, cookie: aToken, header: "wrong-" + aToken}.do(t, h)

	logged := buf.String()

	if !strings.Contains(logged, "csrf check failed") {
		t.Errorf("the refusal was not logged: %s", logged)
	}

	if !strings.Contains(logged, "does not match") {
		t.Errorf("the log does not say which half failed: %s", logged)
	}

	if strings.Contains(logged, aToken) {
		t.Errorf("a token value reached the log: %s", logged)
	}
}
