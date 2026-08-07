package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

// opaqueTokenBytes is the width of every token this application hands out and
// keeps only the digest of: the session cookie, the invitation link, and the
// password reset link to come.
//
// ADR-0005 says 256 bits, which is the same width as the SHA-256 that indexes
// it: guessing a valid token and finding a hash collision are then equally
// hopeless, and neither is the weak half. It is also what the octet_length
// CHECK on every token_hash column in the schema expects.
const opaqueTokenBytes = 32

// errNotBase64 and the length complaint below say only what is wrong with the
// shape, and carry no sentinel of their own. Each caller wraps them in its own
// — a malformed session cookie and a malformed invitation link mean different
// things to whoever is reading the log, even though neither is worth a database
// lookup.
var errNotBase64 = errors.New("it is not base64url")

// newOpaqueToken returns the value to give the client and the digest to store
// beside it.
//
// Only the digest is stored, so a database dump — a backup on a laptop, a
// leaked replica — contains nothing that can be presented as a session or
// redeemed as an invitation.
//
// No salt and no work factor here, unlike a password: the input is 256 bits of
// randomness, so there is no dictionary to precompute and nothing to slow an
// attacker down for.
func newOpaqueToken() (token string, digest []byte, err error) {
	raw := make([]byte, opaqueTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("read token: %w", err)
	}

	sum := sha256.Sum256(raw)

	return base64.RawURLEncoding.EncodeToString(raw), sum[:], nil
}

// decodeOpaqueToken turns a token presented by a client back into the digest
// stored in the matching token_hash column.
//
// The shape is checked first. A cookie or a URL carries whatever the client put
// in it, and a lookup for a value that cannot possibly be a token is a query
// the database should never be asked to run.
func decodeOpaqueToken(token string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, errNotBase64
	}

	if len(raw) != opaqueTokenBytes {
		return nil, fmt.Errorf("it decodes to %d bytes, want %d", len(raw), opaqueTokenBytes)
	}

	sum := sha256.Sum256(raw)

	return sum[:], nil
}
