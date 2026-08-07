// Package fracdex owns the order keys ADR-0003 chose for every ordered list in
// this application: cards, statuses, boards, board columns, checklists and
// checklist items.
//
// A key is a base62 string, and between any two keys there is always room for
// another one. Moving a row therefore rewrites that one row, whatever the
// length of the list, and two people reordering at the same time produce two
// different keys instead of overwriting each other.
//
// Keys are compared byte by byte. That is the comparison PostgreSQL performs
// on the position columns, which are declared text COLLATE "C" precisely so
// the order does not depend on the server locale.
//
// This file defines the format and the gate every key has to pass. Generating
// keys is the other half, and follows.
package fracdex

import (
	"errors"
	"fmt"
	"strings"
)

// digits is base62 in ASCII order, so comparing two keys as bytes also
// compares the numbers they spell.
const digits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// A key is an integer part followed by an optional fraction. The first byte of
// the integer part declares how many digits follow it: 'a' means one, 'b' two,
// up to 'z'; downwards, 'Z' means one, 'Y' two, down to 'A'.
//
// That header is what will keep appending cheap. Without it every key added at
// the end of a list would be one digit longer than the last; with it a list
// runs "a0", "a1" … "az", then "b00", and a thousand appends still fit in
// three bytes.
//
// smallestInteger is the bottom of that space. It is a well formed string but
// never a valid key, because nothing can be inserted before it and a list that
// reached it could never be prepended to again.
const smallestInteger = "A00000000000000000000000000"

// ErrInvalidKey means the input is not a key this package could have produced.
// It is returned rather than tolerated: a malformed key that is stored once
// sorts wrongly forever after.
var ErrInvalidKey = errors.New("order key is malformed")

// Validate reports whether key is one this package could have produced, and is
// what a handler will call before storing a position that came from a client.
// The database constraint only rejects the empty string.
func Validate(key string) error {
	_, _, err := split(key)

	return err
}

// split cuts a key into its integer part and its fraction, rejecting anything
// that is not a key this package could have produced.
func split(key string) (integer, fraction string, err error) {
	if key == "" {
		return "", "", fmt.Errorf("%w: it is empty", ErrInvalidKey)
	}

	if key == smallestInteger {
		return "", "", fmt.Errorf("%w: %q is the floor of the key space, not a key", ErrInvalidKey, key)
	}

	width, err := integerWidth(key[0])
	if err != nil {
		return "", "", err
	}

	if width > len(key) {
		return "", "", fmt.Errorf("%w: %q is shorter than its header claims", ErrInvalidKey, key)
	}

	for i := 0; i < len(key); i++ {
		if strings.IndexByte(digits, key[i]) < 0 {
			return "", "", fmt.Errorf("%w: %q contains %q, which is not a base62 digit", ErrInvalidKey, key, string(key[i]))
		}
	}

	integer, fraction = key[:width], key[width:]

	// A fraction ending in a zero digit names the same position as the same
	// fraction without it. Allowing both would let two rows hold positions
	// that compare equal in every way the algorithm reasons about.
	if strings.HasSuffix(fraction, "0") {
		return "", "", fmt.Errorf("%w: %q ends in a trailing zero", ErrInvalidKey, key)
	}

	return integer, fraction, nil
}

// integerWidth returns the total length of an integer part, header included.
func integerWidth(header byte) (int, error) {
	switch {
	case header >= 'a' && header <= 'z':
		return int(header-'a') + 2, nil
	case header >= 'A' && header <= 'Z':
		return int('Z'-header) + 2, nil
	default:
		return 0, fmt.Errorf("%w: %q is not a length header", ErrInvalidKey, string(header))
	}
}
