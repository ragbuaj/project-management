package mail_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	netmail "net/mail"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ragbuaj/project-management/backend/internal/mail"
)

// fakeServer speaks just enough SMTP to hold one conversation, and lies in
// whichever way a test asks it to.
//
// It is an in-process listener rather than a container so that these tests run
// wherever the rest of them run — CI has PostgreSQL and Redis, it has no mail
// server, and a test that quietly skips is a test that reports ok while proving
// nothing.
type fakeServer struct {
	offerStartTLS bool
	refuseRcpt    bool
	// stall answers the greeting and then says nothing at all, which is what a
	// server that has gone away looks like from the client side.
	stall bool

	listener net.Listener
	done     chan struct{}

	mu            sync.Mutex
	connections   int
	envelopeFrom  string
	envelopeTo    []string
	body          string
	authenticated bool
}

func startFakeServer(t *testing.T, s *fakeServer) *fakeServer {
	t.Helper()

	var lc net.ListenConfig

	listener, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	s.listener = listener
	s.done = make(chan struct{})

	t.Cleanup(func() {
		close(s.done)
		_ = listener.Close()
	})

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			go s.handle(conn)
		}
	}()

	return s
}

func (s *fakeServer) addr(t *testing.T) (host string, port int) {
	t.Helper()

	host, rawPort, err := net.SplitHostPort(s.listener.Addr().String())
	if err != nil {
		t.Fatalf("split listener address: %v", err)
	}

	port, err = strconv.Atoi(rawPort)
	if err != nil {
		t.Fatalf("parse listener port: %v", err)
	}

	return host, port
}

func (s *fakeServer) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	s.mu.Lock()
	s.connections++
	s.mu.Unlock()

	r := bufio.NewReader(conn)
	say := func(format string, args ...any) bool {
		_, err := fmt.Fprintf(conn, format+"\r\n", args...)

		return err == nil
	}

	if !say("220 fake ESMTP ready") {
		return
	}

	if s.stall {
		// Read one command and answer nothing, until the test is over.
		_, _ = r.ReadString('\n')
		<-s.done

		return
	}

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}

		verb, rest, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")

		switch strings.ToUpper(verb) {
		case "EHLO", "HELO":
			// The last line of a multi-line reply is the one with a space.
			if s.offerStartTLS {
				say("250-fake greets you")
				say("250-STARTTLS")
			} else {
				say("250-fake greets you")
			}

			say("250 AUTH PLAIN")
		case "AUTH":
			s.mu.Lock()
			s.authenticated = true
			s.mu.Unlock()
			say("235 2.7.0 accepted")
		case "MAIL":
			// Recorded exactly as it arrived. Pulling the address out of the
			// angle brackets here would forgive a display name in the envelope,
			// which is the mistake a real server rejects the delivery over.
			s.mu.Lock()
			s.envelopeFrom = strings.TrimSpace(rest)
			s.mu.Unlock()
			say("250 2.1.0 sender ok")
		case "RCPT":
			if s.refuseRcpt {
				say("550 5.1.1 no such user here")

				continue
			}

			s.mu.Lock()
			s.envelopeTo = append(s.envelopeTo, strings.TrimSpace(rest))
			s.mu.Unlock()
			say("250 2.1.5 recipient ok")
		case "DATA":
			say("354 go ahead")

			var body strings.Builder

			for {
				dataLine, err := r.ReadString('\n')
				if err != nil {
					return
				}

				if dataLine == ".\r\n" {
					break
				}

				body.WriteString(dataLine)
			}

			s.mu.Lock()
			s.body = body.String()
			s.mu.Unlock()
			say("250 2.0.0 queued")
		case "QUIT":
			say("221 2.0.0 bye")

			return
		default:
			say("250 2.0.0 ok")
		}
	}
}

func (s *fakeServer) received() (from string, to []string, body string, connections int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.envelopeFrom, append([]string(nil), s.envelopeTo...), s.body, s.connections
}

func senderTo(t *testing.T, s *fakeServer, adjust func(*mail.Options)) *mail.SMTP {
	t.Helper()

	host, port := s.addr(t)

	opts := mail.Options{
		Host:             host,
		Port:             port,
		From:             from,
		AllowUnencrypted: true,
	}

	if adjust != nil {
		adjust(&opts)
	}

	sender, err := mail.NewSMTP(opts)
	if err != nil {
		t.Fatalf("NewSMTP: %v", err)
	}

	return sender
}

func TestAMessageReachesTheServerWhole(t *testing.T) {
	t.Parallel()

	server := startFakeServer(t, &fakeServer{})
	sender := senderTo(t, server, nil)

	msg := validMessage()
	msg.To = `"Budi, Ragil" <budi@example.com>`

	if err := sender.Send(t.Context(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	envelopeFrom, envelopeTo, body, _ := server.received()

	// The envelope carries bare addresses. A display name in RCPT TO is how a
	// server is given something it will reject the whole delivery over.
	if want := "FROM:<no-reply@pm.example.com>"; envelopeFrom != want {
		t.Errorf("MAIL %s, want MAIL %s", envelopeFrom, want)
	}

	if want := "TO:<budi@example.com>"; len(envelopeTo) != 1 || envelopeTo[0] != want {
		t.Errorf("RCPT %q, want [%s]", envelopeTo, want)
	}

	// ...while the header keeps the name.
	if !strings.Contains(body, `To: "Budi, Ragil" <budi@example.com>`) {
		t.Errorf("the body does not carry the To header with its name:\n%s", body)
	}

	if !strings.Contains(body, "Subject: You have been invited") {
		t.Errorf("the body does not carry the subject:\n%s", body)
	}
}

// The refusal that keeps a reset link off the open network. Mailpit speaks no
// STARTTLS, and the option that makes it usable locally must not be the default
// anywhere else.
func TestAServerThatCannotEncryptIsRefusedUnlessTheDeploymentSaidSo(t *testing.T) {
	t.Parallel()

	server := startFakeServer(t, &fakeServer{offerStartTLS: false})
	sender := senderTo(t, server, func(o *mail.Options) { o.AllowUnencrypted = false })

	err := sender.Send(t.Context(), validMessage())
	if !errors.Is(err, mail.ErrUndelivered) {
		t.Fatalf("Send error = %v, want ErrUndelivered", err)
	}

	if _, to, body, _ := server.received(); len(to) != 0 || body != "" {
		t.Errorf("the server was given a recipient (%q) or a body (%q) over an unencrypted connection", to, body)
	}
}

func TestARefusedRecipientIsReportedAndNotSwallowed(t *testing.T) {
	t.Parallel()

	server := startFakeServer(t, &fakeServer{refuseRcpt: true})
	sender := senderTo(t, server, nil)

	err := sender.Send(t.Context(), validMessage())
	if !errors.Is(err, mail.ErrUndelivered) {
		t.Fatalf("Send error = %v, want ErrUndelivered", err)
	}

	if _, _, body, _ := server.received(); body != "" {
		t.Errorf("a body was sent after the recipient was refused:\n%s", body)
	}
}

// A message that cannot be rendered is a bug in the caller. Answering it with a
// connection error would send whoever is debugging it to the mail server.
func TestAMessageThatCannotBeRenderedNeverReachesTheNetwork(t *testing.T) {
	t.Parallel()

	server := startFakeServer(t, &fakeServer{})
	sender := senderTo(t, server, nil)

	msg := validMessage()
	msg.Subject = "Reset\r\nBcc: attacker@example.com"

	err := sender.Send(t.Context(), msg)
	if !errors.Is(err, mail.ErrInvalidMessage) {
		t.Fatalf("Send error = %v, want ErrInvalidMessage", err)
	}

	if _, _, _, connections := server.received(); connections != 0 {
		t.Errorf("the server was dialed %d times for a message that was never sendable", connections)
	}
}

// Mail is sent from inside a request handler. A server that accepts the
// connection and then goes quiet must not hold that handler.
func TestASendStopsWhenTheCallerDoes(t *testing.T) {
	t.Parallel()

	server := startFakeServer(t, &fakeServer{stall: true})
	sender := senderTo(t, server, nil)

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)

	go func() { done <- sender.Send(ctx, validMessage()) }()

	// Long enough for the send to be waiting on the server rather than still
	// dialing, short enough that the test is not the slow one.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Send error = %v, want it to report context.Canceled", err)
		}

		if !errors.Is(err, mail.ErrUndelivered) {
			t.Errorf("Send error = %v, want ErrUndelivered as well", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Send is still waiting on a server that stopped answering")
	}
}

func TestOptionsAreCheckedBeforeAnythingCanBeSent(t *testing.T) {
	t.Parallel()

	valid := mail.Options{Host: "smtp.example.com", Port: 587, From: from, Username: "pm", Password: "s3cret"}

	tests := map[string]func(*mail.Options){
		"no host":               func(o *mail.Options) { o.Host = "" },
		"port zero":             func(o *mail.Options) { o.Port = 0 },
		"port out of range":     func(o *mail.Options) { o.Port = 70000 },
		"sender is not one":     func(o *mail.Options) { o.From = netmail.Address{Address: "no-reply"} },
		"password without user": func(o *mail.Options) { o.Username = "" },
		"user without password": func(o *mail.Options) { o.Password = "" },
		// The one that matters: credentials on a connection nobody promised to
		// encrypt are credentials on the wire.
		"credentials in the clear": func(o *mail.Options) { o.AllowUnencrypted = true },
	}

	for name, breakIt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := valid
			breakIt(&opts)

			if _, err := mail.NewSMTP(opts); !errors.Is(err, mail.ErrInvalidOptions) {
				t.Fatalf("NewSMTP error = %v, want ErrInvalidOptions", err)
			}
		})
	}

	if _, err := mail.NewSMTP(valid); err != nil {
		t.Errorf("NewSMTP with valid options: %v", err)
	}
}
