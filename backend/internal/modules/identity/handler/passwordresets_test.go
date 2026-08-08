package handler_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	netmail "net/mail"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ragbuaj/project-management/backend/internal/mail"
	identityhttp "github.com/ragbuaj/project-management/backend/internal/modules/identity/handler"
	identityrepo "github.com/ragbuaj/project-management/backend/internal/modules/identity/repository"
	identitysvc "github.com/ragbuaj/project-management/backend/internal/modules/identity/service"
)

// resetFake answers the store for the request half. It is the invitation fake
// plus an account, because the module has one transaction boundary and
// therefore one store interface.
type resetFake struct {
	inviteFake

	known bool
}

func (f *resetFake) GetUserByEmail(context.Context, string) (identityrepo.GetUserByEmailRow, error) {
	if f.known {
		return identityrepo.GetUserByEmailRow{
			ID:    "0199a1b2-c3d4-7e5f-8a9b-000000000042",
			Email: "budi@example.test",
			Name:  "Budi",
			Role:  "contributor",
		}, nil
	}

	return identityrepo.GetUserByEmailRow{}, pgx.ErrNoRows
}

func (f *resetFake) ExpireOpenPasswordResetsForUser(context.Context, string) (int64, error) {
	return 0, nil
}

func (f *resetFake) CreatePasswordReset(_ context.Context, arg identityrepo.CreatePasswordResetParams) (identityrepo.CreatePasswordResetRow, error) {
	return identityrepo.CreatePasswordResetRow{
		ID:        "0199a1b2-c3d4-7e5f-8a9b-000000000099",
		UserID:    arg.UserID,
		ExpiresAt: arg.ExpiresAt,
	}, nil
}

// stubLimiter is the per-account bucket. It never refuses unless told to.
type stubLimiter struct {
	refuse bool
	wait   time.Duration
}

func (l stubLimiter) Allow(context.Context, string) (bool, time.Duration, error) {
	if l.refuse {
		return false, l.wait, nil
	}

	return true, 0, nil
}

func resetHandler(t *testing.T, store *resetFake, limiter stubLimiter) (*identityhttp.PasswordResets, *mail.Capture) {
	t.Helper()

	capture := mail.NewCapture(netmail.Address{Name: "PM", Address: "no-reply@pm.example.test"})

	base, err := url.Parse("https://pm.example.test")
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	service := identitysvc.NewPasswordResets(
		func(_ context.Context, fn func(identitysvc.TxStore) error) error { return fn(store) },
		capture, limiter, base, log, time.Now)

	return identityhttp.NewPasswordResets(service, log), capture
}

func askForReset(t *testing.T, h *identityhttp.PasswordResets, body string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/auth/password/reset",
		strings.NewReader(body))
	w := httptest.NewRecorder()

	h.Request(w, r)

	return w
}

// The property the endpoint exists to have. Two addresses, one with an account
// and one without, must be indistinguishable from the response alone.
func TestTheAnswerIsTheSameWhetherOrNotTheAddressHasAnAccount(t *testing.T) {
	t.Parallel()

	known, knownMail := resetHandler(t, &resetFake{known: true}, stubLimiter{})
	unknown, unknownMail := resetHandler(t, &resetFake{}, stubLimiter{})

	withAccount := askForReset(t, known, `{"email":"budi@example.test"}`)
	without := askForReset(t, unknown, `{"email":"nobody@example.test"}`)

	if withAccount.Code != http.StatusAccepted {
		t.Fatalf("status = %d for a known address, want 202: %s", withAccount.Code, withAccount.Body)
	}

	if without.Code != withAccount.Code {
		t.Errorf("status = %d for an unknown address and %d for a known one", without.Code, withAccount.Code)
	}

	if without.Body.String() != withAccount.Body.String() {
		t.Errorf("bodies differ: %q against %q", without.Body.String(), withAccount.Body.String())
	}

	// Only the mail tells them apart, and the caller cannot see it.
	if len(knownMail.Sent()) != 1 {
		t.Errorf("%d messages sent for the known address, want 1", len(knownMail.Sent()))
	}

	if len(unknownMail.Sent()) != 0 {
		t.Error("a message was sent for an address with no account")
	}
}

// 202 rather than 200 or 204, and no body at all: what has been accepted is the
// request, and whether a message goes out is not something the caller is told.
func TestAnAcceptedRequestCarriesNoBodyAndNoCookie(t *testing.T) {
	t.Parallel()

	h, _ := resetHandler(t, &resetFake{known: true}, stubLimiter{})

	w := askForReset(t, h, `{"email":"budi@example.test"}`)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}

	if body := strings.TrimSpace(w.Body.String()); body != "" {
		t.Errorf("body = %q, want empty", body)
	}

	// Nothing is signed in by asking. A cookie here would be a session handed to
	// whoever typed an address they do not own.
	if len(w.Result().Cookies()) != 0 {
		t.Error("asking for a reset set a cookie")
	}
}

func TestTheResetRequestBodyIsValidated(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"not json":        {body: `{`, want: http.StatusBadRequest},
		"no email":        {body: `{}`, want: http.StatusBadRequest},
		"empty email":     {body: `{"email":""}`, want: http.StatusBadRequest},
		"not an address":  {body: `{"email":"budi"}`, want: http.StatusBadRequest},
		"a display name":  {body: `{"email":"Budi <budi@example.test>"}`, want: http.StatusBadRequest},
		"a real address":  {body: `{"email":"budi@example.test"}`, want: http.StatusAccepted},
		"unknown address": {body: `{"email":"nobody@example.test"}`, want: http.StatusAccepted},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h, _ := resetHandler(t, &resetFake{known: true}, stubLimiter{})

			if w := askForReset(t, h, tc.body); w.Code != tc.want {
				t.Errorf("status = %d, want %d: %s", w.Code, tc.want, w.Body)
			}
		})
	}
}

// A refusal from the per-account bucket is a 429 with the wait the limiter
// gave, rounded the way every other 429 in this application rounds it.
func TestASpentAccountBucketIsAnsweredWith429AndRetryAfter(t *testing.T) {
	t.Parallel()

	h, capture := resetHandler(t, &resetFake{known: true}, stubLimiter{refuse: true, wait: 90 * time.Second})

	w := askForReset(t, h, `{"email":"budi@example.test"}`)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429: %s", w.Code, w.Body)
	}

	if got := w.Header().Get("Retry-After"); got != "90" {
		t.Errorf("Retry-After = %q, want %q", got, "90")
	}

	if len(capture.Sent()) != 0 {
		t.Error("a message was sent although the bucket refused")
	}
}
