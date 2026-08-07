package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	netmail "net/mail"
	"net/smtp"
	"strconv"
	"time"
)

// ErrInvalidOptions wraps every refusal to build a sender at all. Those are
// deployment mistakes and they surface at start-up.
var ErrInvalidOptions = errors.New("invalid smtp options")

// ErrUndelivered wraps every failure to hand a message to the server. It is
// deliberately distinct from ErrInvalidMessage: one says the message was never
// sendable and retrying it will fail again, the other says this attempt did not
// get through and a later one might.
var ErrUndelivered = errors.New("undelivered")

// sendTimeout bounds a whole delivery — dial, handshake, and the message
// itself. Mail is sent from inside a request handler, and a server that accepts
// the connection and then stops answering would otherwise hold that handler for
// as long as it cared to.
const sendTimeout = 20 * time.Second

// Options describe the server to deliver through.
type Options struct {
	Host string
	Port int

	// From is the address the installation sends as. It belongs here rather
	// than on each message: it is a property of the deployment.
	From netmail.Address

	// Username and Password are empty when the server wants no credentials,
	// which is the case for the local Mailpit and for nothing else.
	Username string
	Password string

	// AllowUnencrypted permits delivering over a connection the server never
	// offered to encrypt. It exists for Mailpit, which speaks neither STARTTLS
	// nor AUTH, and must stay false everywhere a message could reach a real
	// person: without it the message, its recipient, and the reset link inside
	// it cross the network in the clear.
	AllowUnencrypted bool
}

// SMTP delivers messages to one server.
//
// It holds no connection. Mail here is occasional — an invitation, a password
// reset — and a pooled connection to a server that hangs up on idle peers is a
// pool of sockets that fail on first use.
type SMTP struct {
	addr string
	host string
	from netmail.Address
	auth smtp.Auth

	allowUnencrypted bool
}

// NewSMTP validates the options and builds the sender. It contacts nothing:
// a mail server that is down must not be able to stop this application from
// starting, because every part of it that is not mail still works.
func NewSMTP(opts Options) (*SMTP, error) {
	if opts.Host == "" {
		return nil, fmt.Errorf("%w: no host", ErrInvalidOptions)
	}

	if opts.Port <= 0 || opts.Port > 65535 {
		return nil, fmt.Errorf("%w: port %d is not a port", ErrInvalidOptions, opts.Port)
	}

	if _, err := senderAddress(opts.From); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidOptions, err)
	}

	if (opts.Username == "") != (opts.Password == "") {
		return nil, fmt.Errorf("%w: a username and a password come together", ErrInvalidOptions)
	}

	// Credentials over a connection nobody promised to encrypt are credentials
	// on the wire. net/smtp refuses this on its own for PlainAuth, but the
	// refusal belongs here too: it is a deployment mistake, and finding it at
	// start-up is cheaper than finding it the first time somebody is invited.
	if opts.Username != "" && opts.AllowUnencrypted {
		return nil, fmt.Errorf("%w: credentials cannot be sent over an unencrypted connection", ErrInvalidOptions)
	}

	s := &SMTP{
		addr:             net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port)),
		host:             opts.Host,
		from:             opts.From,
		allowUnencrypted: opts.AllowUnencrypted,
	}

	if opts.Username != "" {
		s.auth = smtp.PlainAuth("", opts.Username, opts.Password, opts.Host)
	}

	return s, nil
}

// Send renders the message and delivers it.
//
// Rendering happens first, and before anything is dialed. A message that cannot
// be represented is a bug in the caller, and answering it with a connection
// error would send whoever is debugging it to the mail server.
func (s *SMTP) Send(ctx context.Context, m Message) error {
	raw, err := Render(s.from, m)
	if err != nil {
		return err
	}

	// The envelope recipient is the bare address. The display name belongs in
	// the header, and an SMTP server handed "Budi <budi@example.com>" as a
	// RCPT TO rejects the whole delivery.
	to, err := recipient(m.To)
	if err != nil {
		return err
	}

	sender, err := senderAddress(s.from)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	var dialer net.Dialer

	conn, err := dialer.DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return fmt.Errorf("%w: dial %s: %w", ErrUndelivered, s.addr, err)
	}

	// net/smtp predates context and blocks on reads with no bound of its own.
	// The deadline covers a server that goes quiet; closing the connection when
	// ctx ends covers the caller giving up first — a deadline alone would leave
	// the handler waiting for a request that was already abandoned.
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	if err := s.deliver(conn, sender.Address, to.Address, raw); err != nil {
		_ = conn.Close()

		// A canceled context closes the connection above, and what net/smtp
		// reports for that is a use-of-closed-connection error that says
		// nothing about why. The caller gets the reason it actually stopped.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("%w: %w", ErrUndelivered, ctxErr)
		}

		return err
	}

	return nil
}

// deliver walks the SMTP conversation. It takes the connection rather than
// dialing so the caller keeps the one handle that has to be closed on every
// path out.
func (s *SMTP) deliver(conn net.Conn, from, to string, raw []byte) error {
	client, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return fmt.Errorf("%w: greet %s: %w", ErrUndelivered, s.addr, err)
	}

	// Quit closes the connection on the way out; Close is the fallback for the
	// paths where the conversation is already broken.
	defer func() { _ = client.Close() }()

	if err := s.secure(client); err != nil {
		return err
	}

	if s.auth != nil {
		if err := client.Auth(s.auth); err != nil {
			// The error from a rejected AUTH can quote what was sent.
			return fmt.Errorf("%w: authenticate to %s", ErrUndelivered, s.addr)
		}
	}

	if err := client.Mail(from); err != nil {
		return fmt.Errorf("%w: sender refused: %w", ErrUndelivered, err)
	}

	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("%w: recipient refused: %w", ErrUndelivered, err)
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("%w: start body: %w", ErrUndelivered, err)
	}

	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("%w: write body: %w", ErrUndelivered, err)
	}

	// Close is what sends the terminating dot and reads the server's verdict.
	// A message is delivered here or nowhere.
	if err := w.Close(); err != nil {
		return fmt.Errorf("%w: finish body: %w", ErrUndelivered, err)
	}

	if err := client.Quit(); err != nil {
		return fmt.Errorf("%w: quit: %w", ErrUndelivered, err)
	}

	return nil
}

// secure upgrades the connection, and refuses to continue in the clear unless
// the deployment said so.
func (s *SMTP) secure(client *smtp.Client) error {
	ok, _ := client.Extension("STARTTLS")

	if !ok {
		if s.allowUnencrypted {
			return nil
		}

		return fmt.Errorf("%w: %s does not offer STARTTLS", ErrUndelivered, s.addr)
	}

	// ServerName is the configured host and never anything the server said
	// about itself, which is the whole point of verifying a certificate.
	if err := client.StartTLS(&tls.Config{ServerName: s.host, MinVersion: tls.VersionTLS12}); err != nil {
		return fmt.Errorf("%w: start tls with %s: %w", ErrUndelivered, s.addr, err)
	}

	return nil
}
