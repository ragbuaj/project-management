package domain

import (
	"errors"
	"fmt"
	"time"
)

// PasswordResetWindow is how long a reset link stays usable.
//
// **This number is chosen here, not in an ADR.** ADR-0005 fixes the session
// windows and ADR-0009 rules on the password itself; neither says anything
// about how long the link that replaces one may live.
//
// One hour, against the invitation's seven days, and the difference is not an
// oversight. An invitation is expected to sit in an inbox until somebody gets
// round to it; a reset is requested by somebody who is at the keyboard right
// now, waiting for the mail to arrive. What the shorter window buys is that a
// link forwarded, logged by a mail gateway, or left in a browser history stops
// being a way into the account before the day is out — and unlike an
// invitation, this one opens an account that already has work in it.
const PasswordResetWindow = time.Hour

// ErrPasswordResetTokenMalformed means the value in the link is not shaped like
// a token this application issues. Like its session and invitation
// counterparts it is not proof of an attack — a link broken by a mail client
// that wrapped the URL looks exactly the same — but it is proof that no lookup
// is worth doing.
var ErrPasswordResetTokenMalformed = errors.New("password reset token is malformed")

// PasswordReset is a row of password_resets as the rest of the application
// sees it.
//
// The token is deliberately absent, for the same reason Session and Invitation
// have none: it exists once, in the link that goes out by e-mail, and never
// again. A re-send has to mint a new one, which is the property that makes the
// stored digest worth anything.
//
// There is no e-mail address here either. The row points at an account by id,
// and the address is whatever that account has now — a reset requested before
// an address changed must not resurrect the old one.
type PasswordReset struct {
	ID     string
	UserID string
	// UsedAt is nil while the link is still open. It is what makes a reset
	// single-use: confirming stamps it, and a stamped reset is refused from then
	// on however long it has left.
	UsedAt    *time.Time
	CreatedAt time.Time
	ExpiresAt time.Time
}

// NewPasswordResetToken returns the value to put in the link and the digest to
// store beside it. See newOpaqueToken for why only the digest is kept.
func NewPasswordResetToken() (token string, digest []byte, err error) {
	return newOpaqueToken()
}

// PasswordResetTokenDigest turns a token presented in a link back into the
// digest stored in password_resets.token_hash.
func PasswordResetTokenDigest(token string) ([]byte, error) {
	digest, err := decodeOpaqueToken(token)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPasswordResetTokenMalformed, err)
	}

	return digest, nil
}

// NewPasswordResetExpiry is the deadline a reset gets when it is created.
func NewPasswordResetExpiry(now time.Time) time.Time {
	return now.Add(PasswordResetWindow)
}

// IsExpired reports whether the reset is past its deadline.
func (r PasswordReset) IsExpired(now time.Time) bool {
	return !now.Before(r.ExpiresAt)
}

// IsUsed reports whether the reset has already replaced a password.
func (r PasswordReset) IsUsed() bool {
	return r.UsedAt != nil
}

// IsUsable reports whether this reset may still replace a password.
//
// Both refusals exist and they are separate questions, but callers must answer
// the client with the same thing either way. Here the reason is not the
// enumeration one it is for invitations — whoever holds this link learns
// nothing about which addresses have accounts — but the simpler one: a client
// that could tell "expired" from "never issued" from "already used" could tell
// a token it guessed wrong from a token it guessed right and was too late on.
func (r PasswordReset) IsUsable(now time.Time) bool {
	return !r.IsUsed() && !r.IsExpired(now)
}
