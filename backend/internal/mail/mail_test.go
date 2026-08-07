package mail_test

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/quotedprintable"
	netmail "net/mail"
	"strings"
	"testing"

	"github.com/ragbuaj/project-management/backend/internal/mail"
)

var from = netmail.Address{Name: "Project Management", Address: "no-reply@pm.example.com"}

func validMessage() mail.Message {
	return mail.Message{
		To:      "budi@example.com",
		Subject: "You have been invited",
		Text:    "Open the link to set your password.\nIt expires in 24 hours.",
	}
}

func TestARenderedMessageCarriesTheHeadersAServerExpects(t *testing.T) {
	t.Parallel()

	raw, err := mail.Render(from, validMessage())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	parsed, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("the rendered message does not parse: %v", err)
	}

	for _, want := range []struct{ name, value string }{
		{"To", "<budi@example.com>"},
		{"MIME-Version", "1.0"},
		{"Content-Type", `text/plain; charset="utf-8"`},
		{"Content-Transfer-Encoding", "quoted-printable"},
		{"Auto-Submitted", "auto-generated"},
	} {
		if got := parsed.Header.Get(want.name); got != want.value {
			t.Errorf("%s = %q, want %q", want.name, got, want.value)
		}
	}

	if _, err := parsed.Header.Date(); err != nil {
		t.Errorf("Date does not parse: %v", err)
	}

	sender, err := parsed.Header.AddressList("From")
	if err != nil || len(sender) != 1 || sender[0].Address != from.Address {
		t.Errorf("From = %q, want the installation address %q", parsed.Header.Get("From"), from.Address)
	}

	if id := parsed.Header.Get("Message-ID"); !strings.HasSuffix(id, "@pm.example.com>") {
		t.Errorf("Message-ID = %q, want it to end in the sender's domain", id)
	}
}

func TestTheBodyArrivesAsItWasWritten(t *testing.T) {
	t.Parallel()

	msg := validMessage()
	msg.Text = "Halo Budi,\n\nSandi lama tidak berlaku — buat yang baru.\nBatas 24 jam."

	raw, err := mail.Render(from, msg)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	parsed, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("the rendered message does not parse: %v", err)
	}

	decoded, err := io.ReadAll(quotedprintable.NewReader(parsed.Body))
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}

	// The encoder turns every line break into CRLF, so the comparison is against
	// the text with its own line breaks converted the same way.
	if want := strings.ReplaceAll(msg.Text, "\n", "\r\n"); string(decoded) != want {
		t.Errorf("body = %q, want %q", decoded, want)
	}
}

// A bare LF ends a line for some SMTP servers and does not for others, and a
// message that means different things to two servers is a message that will one
// day be truncated at whatever line the disagreement starts on.
func TestEveryLineEndsWithCRLF(t *testing.T) {
	t.Parallel()

	msg := validMessage()
	msg.Text = "first\nsecond\nthird"

	raw, err := mail.Render(from, msg)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	for i, b := range raw {
		if b == '\n' && (i == 0 || raw[i-1] != '\r') {
			t.Fatalf("byte %d is an LF with no CR before it, in:\n%q", i, raw)
		}
	}
}

// The refusal this package exists for. Every case below is a way to end the
// Subject header early and have the server read what follows as headers of its
// own — a Bcc, another From, a second body.
func TestASubjectCanNeverEndItsOwnHeader(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"CRLF then a header":  "Reset\r\nBcc: attacker@example.com",
		"bare LF":             "Reset\nBcc: attacker@example.com",
		"bare CR":             "Reset\rBcc: attacker@example.com",
		"folded continuation": "Reset\r\n Bcc: attacker@example.com",
		"NUL":                 "Reset\x00Bcc: attacker@example.com",
		"DEL":                 "Reset\x7fBcc: attacker@example.com",
		"leading newline":     "\nBcc: attacker@example.com",
	}

	for name, subject := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			msg := validMessage()
			msg.Subject = subject

			raw, err := mail.Render(from, msg)
			if !errors.Is(err, mail.ErrInvalidMessage) {
				t.Fatalf("Render(%q) error = %v, want ErrInvalidMessage; it rendered:\n%q", subject, err, raw)
			}
		})
	}
}

func TestASubjectSurvivesItsAlphabet(t *testing.T) {
	t.Parallel()

	const subject = "Undangan — buat sandi Anda ✓"

	msg := validMessage()
	msg.Subject = subject

	raw, err := mail.Render(from, msg)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	parsed, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("the rendered message does not parse: %v", err)
	}

	// Raw UTF-8 in a header is 8-bit data on a channel that is not promised to
	// carry it, so it has to have been encoded on the way out...
	encoded := parsed.Header.Get("Subject")
	if strings.Contains(encoded, "—") {
		t.Errorf("Subject = %q, want the non-ASCII text encoded", encoded)
	}

	// ...and still be the same text on the way back in.
	decoded, err := new(mime.WordDecoder).DecodeHeader(encoded)
	if err != nil {
		t.Fatalf("decode Subject: %v", err)
	}

	if decoded != subject {
		t.Errorf("Subject decodes to %q, want %q", decoded, subject)
	}
}

func TestARecipientIsOnePersonAndAnAddress(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"empty":            "",
		"blank":            "   ",
		"not an address":   "budi",
		"two people":       "budi@example.com, ragil@example.com",
		"newline injected": "budi@example.com\r\nBcc: attacker@example.com",
		"no domain":        "budi@",
	}

	for name, to := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			msg := validMessage()
			msg.To = to

			raw, err := mail.Render(from, msg)
			if !errors.Is(err, mail.ErrInvalidMessage) {
				t.Fatalf("Render(To=%q) error = %v, want ErrInvalidMessage; it rendered:\n%q", to, err, raw)
			}
		})
	}
}

// A display name is copied from a user-supplied full name, so it reaches the
// header with whatever the person typed into it.
func TestADisplayNameIsQuotedRatherThanTrusted(t *testing.T) {
	t.Parallel()

	msg := validMessage()
	msg.To = `"Budi, Ragil" <budi@example.com>`

	raw, err := mail.Render(from, msg)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	parsed, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("the rendered message does not parse: %v", err)
	}

	// Unquoted, the comma in the name would make this two addresses.
	list, err := parsed.Header.AddressList("To")
	if err != nil {
		t.Fatalf("parse To: %v", err)
	}

	if len(list) != 1 {
		t.Fatalf("To holds %d addresses, want 1: %q", len(list), parsed.Header.Get("To"))
	}

	if list[0].Address != "budi@example.com" || list[0].Name != "Budi, Ragil" {
		t.Errorf("To = %+v, want the name and address unchanged", *list[0])
	}
}

func TestAMessageWithNothingToSayIsRefused(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*mail.Message){
		"no subject":    func(m *mail.Message) { m.Subject = "" },
		"blank subject": func(m *mail.Message) { m.Subject = "  " },
		"no body":       func(m *mail.Message) { m.Text = "" },
		"long subject":  func(m *mail.Message) { m.Subject = strings.Repeat("a", 201) },
	}

	for name, strip := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			msg := validMessage()
			strip(&msg)

			if _, err := mail.Render(from, msg); !errors.Is(err, mail.ErrInvalidMessage) {
				t.Fatalf("error = %v, want ErrInvalidMessage", err)
			}
		})
	}
}

func TestTheInstallationAddressIsCheckedToo(t *testing.T) {
	t.Parallel()

	tests := map[string]netmail.Address{
		"zero value":     {},
		"no domain":      {Address: "no-reply"},
		"not an address": {Address: "no-reply at example.com"},
		"injected":       {Name: "PM", Address: "no-reply@pm.example.com>\r\nBcc: attacker@example.com"},
	}

	for name, sender := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			raw, err := mail.Render(sender, validMessage())
			if !errors.Is(err, mail.ErrInvalidMessage) {
				t.Fatalf("Render(from=%+v) error = %v, want ErrInvalidMessage; it rendered:\n%q", sender, err, raw)
			}
		})
	}
}

// Two instances of this application send from the same domain and share no
// state, so a receiver that deduplicates on Message-ID would drop the second
// invitation of the day if this repeated.
func TestNoTwoMessagesShareAnIdentifier(t *testing.T) {
	t.Parallel()

	const runs = 100

	seen := make(map[string]bool, runs)

	for range runs {
		raw, err := mail.Render(from, validMessage())
		if err != nil {
			t.Fatalf("Render: %v", err)
		}

		parsed, err := netmail.ReadMessage(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("the rendered message does not parse: %v", err)
		}

		id := parsed.Header.Get("Message-ID")
		if seen[id] {
			t.Fatalf("Message-ID %q was issued twice in %d renders", id, runs)
		}

		seen[id] = true
	}
}
