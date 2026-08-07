package mail_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/ragbuaj/project-management/backend/internal/mail"
)

func TestACapturedMessageIsKeptWholeAndRendered(t *testing.T) {
	t.Parallel()

	capture := mail.NewCapture(from)

	if err := capture.Send(t.Context(), validMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	last, ok := capture.Last()
	if !ok {
		t.Fatal("nothing was captured")
	}

	if last.Message != validMessage() {
		t.Errorf("captured %+v, want the message as it was handed over", last.Message)
	}

	if !strings.Contains(string(last.Raw), "Subject: You have been invited") {
		t.Errorf("the raw form does not carry the subject:\n%q", last.Raw)
	}
}

// The property that makes this fake worth having. A capture that only appended
// to a slice would let a test pass on a message the SMTP sender refuses, and
// the injection would be found in production instead.
func TestTheCaptureRefusesWhatTheSenderWouldRefuse(t *testing.T) {
	t.Parallel()

	capture := mail.NewCapture(from)

	msg := validMessage()
	msg.Subject = "Reset\r\nBcc: attacker@example.com"

	if err := capture.Send(t.Context(), msg); !errors.Is(err, mail.ErrInvalidMessage) {
		t.Fatalf("Send error = %v, want ErrInvalidMessage", err)
	}

	if sent := capture.Sent(); len(sent) != 0 {
		t.Errorf("captured %d messages, want the refused one recorded nowhere", len(sent))
	}
}

func TestACanceledContextSendsNothing(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	capture := mail.NewCapture(from)

	if err := capture.Send(ctx, validMessage()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Send error = %v, want context.Canceled", err)
	}

	if sent := capture.Sent(); len(sent) != 0 {
		t.Errorf("captured %d messages, want none", len(sent))
	}
}

func TestASendingFailureCanBeAskedFor(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("the server hung up")

	capture := mail.NewCapture(from)
	capture.Fail(wantErr)

	if err := capture.Send(t.Context(), validMessage()); !errors.Is(err, wantErr) {
		t.Fatalf("Send error = %v, want %v", err, wantErr)
	}

	if sent := capture.Sent(); len(sent) != 0 {
		t.Fatalf("captured %d messages, want none while failing", len(sent))
	}

	capture.Fail(nil)

	if err := capture.Send(t.Context(), validMessage()); err != nil {
		t.Fatalf("Send after Fail(nil): %v", err)
	}

	if sent := capture.Sent(); len(sent) != 1 {
		t.Errorf("captured %d messages, want 1 once it accepts again", len(sent))
	}
}

// Sent returns a copy so that a test holding the result while the code under
// test keeps sending does not read a slice being appended to underneath it.
func TestTheRecordHandedOutIsACopy(t *testing.T) {
	t.Parallel()

	capture := mail.NewCapture(from)

	if err := capture.Send(t.Context(), validMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := capture.Sent()
	got[0].Message.Subject = "overwritten"

	again, _ := capture.Last()
	if again.Message.Subject != validMessage().Subject {
		t.Errorf("the captured subject became %q; the record handed out was the record itself", again.Message.Subject)
	}
}

// Mail is sent from request handlers, and handlers run concurrently.
func TestManySendersAtOnceLoseNothing(t *testing.T) {
	t.Parallel()

	const senders = 50

	capture := mail.NewCapture(from)

	var wg sync.WaitGroup

	for range senders {
		wg.Go(func() {
			if err := capture.Send(t.Context(), validMessage()); err != nil {
				t.Errorf("Send: %v", err)
			}
		})
	}

	wg.Wait()

	if sent := capture.Sent(); len(sent) != senders {
		t.Errorf("captured %d messages, want %d", len(sent), senders)
	}
}
