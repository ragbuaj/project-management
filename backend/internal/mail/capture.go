package mail

import (
	"context"
	netmail "net/mail"
	"sync"
)

// Delivery is one message a Capture accepted, kept both as it was handed over
// and as it was rendered.
type Delivery struct {
	Message Message
	Raw     []byte
}

// Capture is a sender that keeps messages in memory instead of delivering them.
// It exists so the tests of everything that sends mail — invitations, password
// resets — can assert on what was sent without an SMTP server.
//
// It renders every message through Render, and that is the point of it rather
// than an implementation detail. A capture that only appended to a slice would
// accept a subject with a newline in it and let a test pass on a message the
// real sender refuses, which is the one bug a fake sender must not be able to
// hide.
type Capture struct {
	from netmail.Address

	mu   sync.Mutex
	sent []Delivery
	err  error
}

// NewCapture builds a capture that renders as if it sent from.
func NewCapture(from netmail.Address) *Capture {
	return &Capture{from: from}
}

// Send renders the message and records it.
//
// It honors ctx, which a fake has no technical need to do. Code that hands a
// canceled context to its sender is code that would hang or send late against
// the real one, and a fake that ignored the context would let that ship.
func (c *Capture) Send(ctx context.Context, m Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.err != nil {
		return c.err
	}

	raw, err := Render(c.from, m)
	if err != nil {
		return err
	}

	c.sent = append(c.sent, Delivery{Message: m, Raw: raw})

	return nil
}

// Fail makes every later Send return err without recording anything. Passing
// nil goes back to accepting.
func (c *Capture) Fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.err = err
}

// Sent returns everything recorded so far, oldest first.
func (c *Capture) Sent() []Delivery {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Copied: the caller is a test that will read this while whatever it is
	// testing may still be sending.
	out := make([]Delivery, len(c.sent))
	copy(out, c.sent)

	return out
}

// Last returns the most recent message, and whether there was one.
func (c *Capture) Last() (Delivery, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.sent) == 0 {
		return Delivery{}, false
	}

	return c.sent[len(c.sent)-1], true
}
