// Package mail turns the few messages this application sends — invitations and
// password resets — into something an SMTP server will accept, and delivers
// them.
//
// It deliberately does not template anything. The bodies are short, they are
// written where the feature that sends them lives, and a template engine here
// would only move that text one indirection away from the code that decides
// what it says.
package mail

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	netmail "net/mail"
	"strings"
	"time"
)

// ErrInvalidMessage wraps every refusal to render, so a caller can tell a
// message it built wrong from a server that would not take it.
var ErrInvalidMessage = errors.New("invalid message")

// maxSubjectBytes keeps the rendered Subject header inside the 998-byte line
// limit RFC 5322 puts on every header. Encoding can multiply the length of
// non-ASCII text several times over, so the limit is on the input and is set
// far enough below the ceiling that no alphabet can cross it. Nothing this
// application sends comes close.
const maxSubjectBytes = 200

// Message is one e-mail addressed to one person.
//
// One recipient, never a list: every message this application sends is about
// the person receiving it, and a second address on the To line would tell each
// of them who else was invited or asked to reset a password.
//
// It carries no From. That address belongs to the installation rather than to
// any message, and a service that could choose it is a service that could get
// it wrong.
type Message struct {
	To      string
	Subject string
	Text    string
}

// Render turns the message into RFC 5322 wire format, sent as it is from.
//
// It refuses anything it cannot represent honestly rather than dropping the
// offending part. The refusals matter most on Subject: an unchecked newline
// there ends the header and everything after it is read by the server as more
// headers — a Bcc to somebody else, a different From — which is how a password
// reset becomes a way to send mail from this installation to anyone.
func Render(from netmail.Address, m Message) ([]byte, error) {
	sender, err := senderAddress(from)
	if err != nil {
		return nil, err
	}

	to, err := recipient(m.To)
	if err != nil {
		return nil, err
	}

	subject, err := subjectHeader(m.Subject)
	if err != nil {
		return nil, err
	}

	if m.Text == "" {
		return nil, fmt.Errorf("%w: the body is empty", ErrInvalidMessage)
	}

	id, err := messageID(sender.Address)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer

	// Date and Message-ID are here because a message without them is scored as
	// spam by most receivers, and this application's mail is the one mail a new
	// employee has to receive to be able to sign in at all.
	header(&buf, "Date", time.Now().Format(time.RFC1123Z))
	header(&buf, "From", sender.String())
	header(&buf, "To", to.String())
	header(&buf, "Subject", subject)
	header(&buf, "Message-ID", id)
	header(&buf, "MIME-Version", "1.0")
	header(&buf, "Content-Type", `text/plain; charset="utf-8"`)
	header(&buf, "Content-Transfer-Encoding", "quoted-printable")
	// Nothing here is worth an out-of-office reply, and an auto-reply to an
	// invitation would arrive at an address nobody reads.
	header(&buf, "Auto-Submitted", "auto-generated")

	buf.WriteString("\r\n")

	// Quoted-printable rather than base64: the text stays readable in a wire
	// dump and in Mailpit, which is where every one of these messages is looked
	// at during development. It also normalises line endings to CRLF, which the
	// body needs and Go's own strings do not have.
	w := quotedprintable.NewWriter(&buf)

	if _, err := w.Write([]byte(m.Text)); err != nil {
		return nil, fmt.Errorf("encode body: %w", err)
	}

	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("encode body: %w", err)
	}

	return buf.Bytes(), nil
}

func header(buf *bytes.Buffer, name, value string) {
	buf.WriteString(name)
	buf.WriteString(": ")
	buf.WriteString(value)
	buf.WriteString("\r\n")
}

// senderAddress checks the address the installation sends as.
//
// It is validated on every render rather than once at start-up because a
// netmail.Address is a plain struct: any caller can build one with a hand-typed
// string in it, and the zero value is an empty address that an SMTP server
// would take as a bounce notification.
func senderAddress(from netmail.Address) (netmail.Address, error) {
	if err := noControl("sender name", from.Name); err != nil {
		return netmail.Address{}, err
	}

	if err := noControl("sender address", from.Address); err != nil {
		return netmail.Address{}, err
	}

	parsed, err := netmail.ParseAddress(from.String())
	if err != nil {
		return netmail.Address{}, fmt.Errorf("%w: the sender address is not an address", ErrInvalidMessage)
	}

	if _, _, ok := split(parsed.Address); !ok {
		return netmail.Address{}, fmt.Errorf("%w: the sender address has no domain", ErrInvalidMessage)
	}

	return *parsed, nil
}

// recipient parses the To address.
//
// The parsed value is re-encoded through Address.String() rather than passed
// through, because that is what quotes a display name and encodes a non-ASCII
// one. Writing the caller's string into the header as it stands is the second
// half of the injection Subject is guarded against.
func recipient(raw string) (netmail.Address, error) {
	if strings.TrimSpace(raw) == "" {
		return netmail.Address{}, fmt.Errorf("%w: there is no recipient", ErrInvalidMessage)
	}

	if err := noControl("recipient", raw); err != nil {
		return netmail.Address{}, err
	}

	parsed, err := netmail.ParseAddress(raw)
	if err != nil {
		return netmail.Address{}, fmt.Errorf("%w: the recipient is not an address", ErrInvalidMessage)
	}

	return *parsed, nil
}

// subjectHeader refuses a subject that would not survive being written on one
// header line, and encodes the rest.
//
// mime.QEncoding leaves ASCII alone, which is exactly why the control-character
// check has to happen first: a subject of "Reset\r\nBcc: someone@example.com"
// is pure ASCII and would be copied into the header untouched.
func subjectHeader(subject string) (string, error) {
	if strings.TrimSpace(subject) == "" {
		return "", fmt.Errorf("%w: the subject is empty", ErrInvalidMessage)
	}

	if len(subject) > maxSubjectBytes {
		return "", fmt.Errorf("%w: the subject is longer than %d bytes", ErrInvalidMessage, maxSubjectBytes)
	}

	if err := noControl("subject", subject); err != nil {
		return "", err
	}

	return mime.QEncoding.Encode("utf-8", subject), nil
}

// noControl refuses any value carrying a character that must never reach a
// header: the C0 set, which includes CR and LF, and DEL.
//
// Refusing is not the only safe answer — netmail.Address.String() neutralizes an
// injected address by quoting the whole thing into the local part, and
// ParseAddress then folds the line break away. But what comes out the other side
// of that is a valid header naming an address nobody meant to send from, and a
// message that goes out under a mangled sender is worse than one that never
// leaves. The refusal is also the claim a test can hold on to: quoting rules are
// the library's to change, and this is ours.
func noControl(field, value string) error {
	if i := strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }); i >= 0 {
		return fmt.Errorf("%w: the %s contains a control character at byte %d", ErrInvalidMessage, field, i)
	}

	return nil
}

// messageID builds a globally unique identifier for this message, in the domain
// the installation sends from.
//
// The random half is 16 bytes from crypto/rand rather than a counter or a
// timestamp: two instances of this application send from the same domain and
// share no state, and a repeated Message-ID lets a receiver treat the second
// message as a duplicate it has already delivered.
func messageID(sender string) (string, error) {
	_, domain, ok := split(sender)
	if !ok {
		return "", fmt.Errorf("%w: the sender address has no domain", ErrInvalidMessage)
	}

	var random [16]byte

	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}

	return "<" + hex.EncodeToString(random[:]) + "@" + domain + ">", nil
}

// split separates an addr-spec into its local part and its domain. It splits at
// the last @ because the local part is allowed to contain one when quoted.
func split(address string) (local, domain string, ok bool) {
	at := strings.LastIndex(address, "@")
	if at <= 0 || at == len(address)-1 {
		return "", "", false
	}

	return address[:at], address[at+1:], true
}
