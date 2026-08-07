package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

// The two windows ADR-0005 sets. Idle is how long a session survives without
// being used; absolute is how long it may live at all, however active.
//
// Both are needed. Idle alone means a stolen cookie that is used regularly
// never expires. Absolute alone means someone who works every day is signed
// out mid-sprint on a fixed date, which teaches people to keep a second tab
// signed in somewhere less careful.
const (
	SessionIdleWindow     = 14 * 24 * time.Hour
	SessionAbsoluteWindow = 90 * 24 * time.Hour

	// A session is only written back when sliding its deadline would move it
	// by more than this. Renewing on every request would mean one UPDATE per
	// request for the whole application — the cost of a login page that is
	// never reloaded, paid on every card drag.
	//
	// The price of not writing is that a deadline can be up to this much
	// earlier than the idle window promises. An hour against fourteen days is
	// not a difference a person can notice.
	sessionRenewalFloor = time.Hour
)

// sessionTokenBytes is the entropy behind the cookie. ADR-0005 says 256 bits,
// which is the same width as the SHA-256 that indexes it: guessing a valid
// token and finding a hash collision are then equally hopeless, and neither is
// the weak half.
const sessionTokenBytes = 32

var (
	// ErrSessionTokenMalformed means the value is not shaped like a token this
	// application issues. It is not proof of an attack — an old cookie from
	// before a format change looks exactly the same — but it is proof that no
	// lookup is worth doing.
	ErrSessionTokenMalformed = errors.New("session token is malformed")

	// ErrSessionExpired means the session was found but may no longer be used.
	// Kept separate from "no such session" for the log, never for the answer:
	// both must reach the client as the same 401.
	ErrSessionExpired = errors.New("session has expired")
)

// Session is a row of sessions, as the rest of the application sees it. The
// token itself is deliberately absent: it exists once, in the response that
// sets the cookie, and never again.
type Session struct {
	ID         string
	UserID     string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// NewSessionToken returns the value to put in the cookie and the digest to
// store beside it.
//
// Only the digest is stored, so a database dump — a backup on a laptop, a
// leaked replica — does not contain anything that can be presented as a
// session. This is the same reason ADR-0005 stores API tokens and share links
// as digests.
//
// No salt and no work factor here, unlike a password: the input is 256 bits of
// randomness, so there is no dictionary to precompute and nothing to slow an
// attacker down for.
func NewSessionToken() (token string, digest []byte, err error) {
	raw := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("read session token: %w", err)
	}

	sum := sha256.Sum256(raw)

	return base64.RawURLEncoding.EncodeToString(raw), sum[:], nil
}

// SessionTokenDigest turns a token presented by a client back into the digest
// stored in sessions.token_hash.
//
// The shape is checked first. A cookie carries whatever the client puts in it,
// and a lookup for a value that cannot possibly be a token is a query the
// database should never be asked to run.
func SessionTokenDigest(token string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("%w: it is not base64url", ErrSessionTokenMalformed)
	}

	if len(raw) != sessionTokenBytes {
		return nil, fmt.Errorf("%w: it decodes to %d bytes, want %d", ErrSessionTokenMalformed, len(raw), sessionTokenBytes)
	}

	sum := sha256.Sum256(raw)

	return sum[:], nil
}

// NewSessionExpiry is the deadline a session gets when it is created.
func NewSessionExpiry(now time.Time) time.Time {
	return now.Add(SessionIdleWindow)
}

// IsExpired reports whether the session may still be used.
//
// One comparison covers both windows, because RenewedExpiry never moves a
// deadline past the absolute one. A row whose expires_at is in the future is
// therefore usable without consulting created_at again — including rows the
// expiry sweep has not reached yet.
func (s Session) IsExpired(now time.Time) bool {
	return !now.Before(s.ExpiresAt)
}

// RenewedExpiry is where the deadline moves when the session is used.
//
// The absolute window is the ceiling, so a session that has been alive for 89
// days gets one more day rather than fourteen more.
func (s Session) RenewedExpiry(now time.Time) time.Time {
	absolute := s.CreatedAt.Add(SessionAbsoluteWindow)

	renewed := now.Add(SessionIdleWindow)
	if renewed.After(absolute) {
		return absolute
	}

	return renewed
}

// NeedsRenewal reports whether the deadline has moved far enough to be worth a
// write. A session already at its absolute ceiling never does, so the last day
// of a long-lived session costs no writes at all.
func (s Session) NeedsRenewal(now time.Time) bool {
	return s.RenewedExpiry(now).Sub(s.ExpiresAt) > sessionRenewalFloor
}
