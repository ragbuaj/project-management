package domain

import (
	"errors"
	"fmt"
	"time"
)

// InvitationWindow is how long an invitation link stays usable.
//
// **This number is chosen here, not in an ADR.** ADR-0005 fixes the session
// windows and says nothing about invitations. Seven days is long enough to
// survive somebody being invited on a Friday afternoon or during a week of
// leave, and short enough that a link forwarded into a group chat and forgotten
// stops working before anyone remembers it exists. It is worth revisiting the
// first time an employee has to ask for a second invitation.
const InvitationWindow = 7 * 24 * time.Hour

// ErrInvitationTokenMalformed means the value in the link is not shaped like a
// token this application issues. Like its session counterpart it is not proof
// of an attack — a link broken by an e-mail client that wrapped the URL looks
// exactly the same — but it is proof that no lookup is worth doing.
var ErrInvitationTokenMalformed = errors.New("invitation token is malformed")

// Invitation is a row of invitations as the rest of the application sees it.
//
// The token is deliberately absent, for the same reason Session has no token:
// it exists once, in the link that goes out by e-mail, and never again. Not
// even the owner who sent it can read it back — a re-send has to mint a new
// one, which is the property that makes the stored digest worth anything.
type Invitation struct {
	ID    string
	Email string
	// Role is the ACCOUNT role the invitee's account will be created with
	// (ADR-0012), never a role inside a project. `owner` is not among the values
	// the schema accepts: there is exactly one owner and that account is the
	// first one, not an invited one.
	Role      string
	InvitedBy string
	CreatedAt time.Time
	ExpiresAt time.Time
	// AcceptedAt is nil while the invitation is still open. It is what makes an
	// invitation single-use: accepting stamps it, and a stamped invitation is
	// refused from then on however long it has left.
	AcceptedAt *time.Time
}

// NewInvitationToken returns the value to put in the link and the digest to
// store beside it. See newOpaqueToken for why only the digest is kept.
func NewInvitationToken() (token string, digest []byte, err error) {
	return newOpaqueToken()
}

// InvitationTokenDigest turns a token presented in a link back into the digest
// stored in invitations.token_hash.
func InvitationTokenDigest(token string) ([]byte, error) {
	digest, err := decodeOpaqueToken(token)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvitationTokenMalformed, err)
	}

	return digest, nil
}

// NewInvitationExpiry is the deadline an invitation gets when it is created.
func NewInvitationExpiry(now time.Time) time.Time {
	return now.Add(InvitationWindow)
}

// IsExpired reports whether the invitation is past its deadline.
func (i Invitation) IsExpired(now time.Time) bool {
	return !now.Before(i.ExpiresAt)
}

// IsAccepted reports whether the invitation has already been redeemed.
func (i Invitation) IsAccepted() bool {
	return i.AcceptedAt != nil
}

// IsUsable reports whether this invitation may still create an account.
//
// Both refusals exist and they are separate questions, but callers must answer
// the client with the same thing either way: telling somebody holding a link
// that it is "expired" rather than "unknown" confirms the address was invited,
// which is the enumeration docs/threat-model.md closes on this path.
func (i Invitation) IsUsable(now time.Time) bool {
	return !i.IsAccepted() && !i.IsExpired(now)
}
