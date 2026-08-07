// Package domain holds the identity entities and the rules that hold whatever
// stores them. It imports nothing else from this repository (ADR-0008).
package domain

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"
)

// Argon2id, as rules/40-security.md and ADR-0005 require. The parameters are
// the ones OWASP recommends for the second of its accepted configurations:
// 19 MiB of memory, two passes, no parallelism.
//
// Memory is the expensive half on purpose. An attacker with a GPU can buy
// arithmetic far more cheaply than memory bandwidth, which is the whole reason
// this application does not hash passwords with SHA-2 and a salt.
//
// The cost is paid on this server too: one login occupies 19 MiB for as long
// as the hash runs. Ten at once is 190 MiB, which is why the rate limit on the
// login endpoint is not only an anti-guessing measure.
const (
	argonMemoryKiB = 19 * 1024
	argonTime      = 2
	argonThreads   = 1

	saltLength = 16
	keyLength  = 32
)

// The length policy, from ADR-0009.
//
// Length is the only requirement that adds real entropy, which is why it is
// the only one here: no composition rules, no character classes, nothing that
// turns a password into Password1!.
const (
	// MinPasswordLength counts characters, not bytes, and is counted after
	// normalisation. OWASP ASVS puts the practical minimum here; NIST allows 8
	// and recommends 15 or more.
	MinPasswordLength = 12

	// MaxPasswordLength bounds what will be hashed, in bytes. ADR-0009 asks
	// for a maximum far above 64 characters — this is comfortably that, since
	// even 64 four-byte runes are 256 bytes.
	//
	// A password over the limit is refused, never truncated. A silently
	// truncated password is a different password from the one that was typed,
	// and its owner will never learn why the other device cannot sign in.
	//
	// Argon2 has no input limit of its own, so without a bound here a request
	// body could ask this server to hash a megabyte.
	MaxPasswordLength = 1024
)

var (
	// ErrPasswordMismatch means the password does not match the hash. The
	// caller must answer it exactly as it answers an address that has no
	// account at all — ADR-0005 requires the two to be indistinguishable, or
	// the login endpoint becomes an account enumerator.
	ErrPasswordMismatch = errors.New("password does not match")

	// ErrPasswordTooLong means the input exceeds MaxPasswordLength. It is
	// returned rather than the password being cut down to fit.
	ErrPasswordTooLong = errors.New("password is longer than the limit")

	// ErrPasswordTooShort means the input is under MinPasswordLength
	// characters.
	ErrPasswordTooShort = errors.New("password is shorter than the minimum")

	// ErrHashUnreadable means the stored hash is not one this package wrote.
	// It is a corrupted row or a hash from another application, never a wrong
	// password, and the two must not be reported the same way: one is a failed
	// login, the other is an operator's problem.
	ErrHashUnreadable = errors.New("stored password hash is unreadable")
)

// ValidatePassword applies the length policy and returns the password in the
// form that will be hashed.
//
// Normalisation is NFKC and it happens here, before anything counts characters
// or hashes bytes. Without it the same password typed on a different keyboard
// — a composed é against an e with a combining accent — is a different byte
// string, and its owner is locked out of half their devices with no
// explanation. It is also why the choice cannot be revisited later: changing
// it stops every stored hash from matching.
//
// Nothing else is checked. ADR-0009 rules out composition requirements, and
// the breach blocklist is a network call that belongs to the service rather
// than to a pure function.
func ValidatePassword(plain string) (string, error) {
	normalised := norm.NFKC.String(plain)

	// Bytes, because this is the bound that protects the server: it is what
	// stops a request body from asking for a megabyte to be hashed.
	if len(normalised) > MaxPasswordLength {
		return "", ErrPasswordTooLong
	}

	// Characters, because this is the bound that protects the account, and a
	// person choosing a password counts characters rather than bytes. Counting
	// bytes here would let "日本語パスワード" through at three characters.
	if utf8.RuneCountInString(normalised) < MinPasswordLength {
		return "", ErrPasswordTooShort
	}

	return normalised, nil
}

// HashPassword returns the encoded hash to store in users.password_hash.
//
// The encoding is the PHC string format — "$argon2id$v=19$m=…,t=…,p=…$salt$hash"
// — so every hash carries the parameters it was made with. Raising the cost
// later therefore does not invalidate the hashes already stored: they keep
// verifying with their own parameters, and NeedsRehash reports which ones are
// worth rewriting at the next successful login.
func HashPassword(plain string) (string, error) {
	normalised, err := ValidatePassword(plain)
	if err != nil {
		return "", err
	}

	plain = normalised

	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("read salt: %w", err)
	}

	key := argon2.IDKey([]byte(plain), salt, argonTime, argonMemoryKiB, argonThreads, keyLength)

	return encode(argonMemoryKiB, argonTime, argonThreads, salt, key), nil
}

// VerifyPassword reports whether plain is the password behind encoded.
//
// It recomputes with the parameters stored in the hash rather than the current
// ones, so a hash written before a cost increase still verifies. The comparison
// is constant time: a byte-by-byte one leaks how much of a guess was right,
// which is enough to reconstruct the digest one byte at a time.
func VerifyPassword(encoded, plain string) error {
	memoryKiB, passes, threads, salt, want, err := decode(encoded)
	if err != nil {
		return err
	}

	// Normalised the same way it was when it was hashed, or the same password
	// typed on a different keyboard produces different bytes and fails. The
	// minimum length is deliberately not enforced here: a password stored
	// before the policy changed still has to let its owner in, and a guess
	// that is too short is a failed login rather than a policy error.
	plain = norm.NFKC.String(plain)

	if len(plain) > MaxPasswordLength {
		return ErrPasswordMismatch
	}

	// decode has already bounded this, but the bound is restated on the value
	// that actually crosses into a uint32: a length that wrapped would ask
	// argon2 for the wrong digest size.
	size := len(want)
	if size < keyLength/2 || size > keyLength*2 {
		return fmt.Errorf("%w: hash is %d bytes", ErrHashUnreadable, size)
	}

	got := argon2.IDKey([]byte(plain), salt, passes, memoryKiB, threads, uint32(size))

	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrPasswordMismatch
	}

	return nil
}

// NeedsRehash reports whether encoded was written with parameters weaker than
// the ones in force now. A hash that cannot be read at all needs replacing
// too, and says so.
//
// The only moment a stored hash can be upgraded is a successful login, because
// that is the only moment the password is in memory. A caller that never asks
// keeps its oldest accounts at the cost they were created with.
func NeedsRehash(encoded string) bool {
	memoryKiB, passes, threads, _, key, err := decode(encoded)
	if err != nil {
		return true
	}

	return memoryKiB < argonMemoryKiB ||
		passes < argonTime ||
		threads != argonThreads ||
		len(key) != keyLength
}

// encode writes the PHC string. base64 here is the unpadded standard alphabet,
// which is what the Argon2 reference implementation and every other PHC reader
// expects.
func encode(memoryKiB, passes uint32, threads uint8, salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, memoryKiB, passes, threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key))
}

// decode reads a PHC string back. Everything it rejects is a hash this package
// did not write, so the error never means "wrong password".
func decode(encoded string) (memoryKiB, passes uint32, threads uint8, salt, key []byte, err error) {
	fields := strings.Split(encoded, "$")
	if len(fields) != 6 || fields[0] != "" {
		return 0, 0, 0, nil, nil, fmt.Errorf("%w: it has %d fields", ErrHashUnreadable, len(fields))
	}

	if fields[1] != "argon2id" {
		return 0, 0, 0, nil, nil, fmt.Errorf("%w: algorithm is %q, not argon2id", ErrHashUnreadable, fields[1])
	}

	var version int
	if _, err := fmt.Sscanf(fields[2], "v=%d", &version); err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("%w: version field is %q", ErrHashUnreadable, fields[2])
	}

	// A hash from a future Argon2 version would verify to nothing useful here,
	// and reporting that as a wrong password would lock every account out with
	// no explanation in the log.
	if version != argon2.Version {
		return 0, 0, 0, nil, nil, fmt.Errorf("%w: version %d, want %d", ErrHashUnreadable, version, argon2.Version)
	}

	if _, err := fmt.Sscanf(fields[3], "m=%d,t=%d,p=%d", &memoryKiB, &passes, &threads); err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("%w: parameter field is %q", ErrHashUnreadable, fields[3])
	}

	if memoryKiB == 0 || passes == 0 || threads == 0 {
		return 0, 0, 0, nil, nil, fmt.Errorf("%w: parameters are %q", ErrHashUnreadable, fields[3])
	}

	// argon2.IDKey allocates memory KiB and spawns threads goroutines. A row
	// that asks for a terabyte is not a hash to verify, it is a way to take
	// this process down with one login attempt.
	if memoryKiB > argonMemoryKiB*maxCostFactor || passes > argonTime*maxCostFactor || int(threads) > runtime.NumCPU() {
		return 0, 0, 0, nil, nil, fmt.Errorf("%w: parameters %q are beyond what this application writes", ErrHashUnreadable, fields[3])
	}

	if salt, err = base64.RawStdEncoding.DecodeString(fields[4]); err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("%w: salt is not base64", ErrHashUnreadable)
	}

	if key, err = base64.RawStdEncoding.DecodeString(fields[5]); err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("%w: hash is not base64", ErrHashUnreadable)
	}

	// Bounds rather than an exact match, so a hash written before a length
	// change still verifies — but nothing absurd reaches argon2.IDKey, whose
	// last argument is the number of bytes it is asked to produce.
	if len(salt) < saltLength || len(key) < keyLength/2 || len(key) > keyLength*2 {
		return 0, 0, 0, nil, nil, fmt.Errorf("%w: salt is %d bytes and hash is %d", ErrHashUnreadable, len(salt), len(key))
	}

	return memoryKiB, passes, threads, salt, key, nil
}

// maxCostFactor is how far above the current cost a stored hash may ask this
// server to go. Room for a few deliberate increases, not for an arbitrary
// number read out of a table.
const maxCostFactor = 8
