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
// The layout is the one the fractional-indexing package on the web client
// uses, so the key a browser computes for an optimistic drag is the key the
// server would have computed. The single exception is documented on Between.
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
// That header is what keeps appending cheap. Without it every key added at the
// end of a list would be one digit longer than the last; with it a list runs
// "a0", "a1" … "az", then "b00", and a thousand appends still fit in three
// bytes.
const (
	// firstKey is what an empty list starts with: the middle of the space, so
	// there is as much room below it as above.
	firstKey = "a0"

	// smallestInteger is the bottom of that space. It is a well formed string
	// but never a valid key, because nothing can be inserted before it and a
	// list that reached it could never be prepended to again.
	smallestInteger = "A00000000000000000000000000"
)

var (
	// ErrInvalidKey means the input is not a key this package could have
	// produced. It is returned rather than tolerated: a malformed key that is
	// stored once sorts wrongly forever after.
	ErrInvalidKey = errors.New("order key is malformed")

	// ErrNotAscending means the caller passed the two bounds the wrong way
	// round, or passed the same key twice. Nothing sorts strictly between a
	// key and itself.
	ErrNotAscending = errors.New("order keys are not in ascending order")

	// ErrExhausted means the integer space ran out at that end. Reaching it
	// takes on the order of 62^26 insertions, so it is a guard against a
	// corrupted key reaching this package, not an outcome to plan for.
	ErrExhausted = errors.New("order key space is exhausted")
)

// Validate reports whether key is one this package could have produced, and is
// what a handler will call before storing a position that came from a client.
// The database constraint only rejects the empty string.
func Validate(key string) error {
	_, _, err := split(key)

	return err
}

// Between returns a key that sorts strictly after prev and strictly before
// next. An empty prev means "nothing sorts before it", an empty next means
// "nothing sorts after it"; Between("", "") is the first key of a new list.
// The empty string is never itself a key, which is why it can carry that
// meaning.
//
// The result is always a key Validate accepts. That is the one place this
// implementation departs from the web client's fractional-indexing package,
// which in the single corner where prepending reaches the bottom of the
// integer space returns a string its own validator rejects. Getting there
// requires 62^26 prepends, so the two agree on every key either will compute.
func Between(prev, next string) (string, error) {
	prevInt, prevFrac, err := splitBound(prev)
	if err != nil {
		return "", fmt.Errorf("lower bound: %w", err)
	}

	nextInt, nextFrac, err := splitBound(next)
	if err != nil {
		return "", fmt.Errorf("upper bound: %w", err)
	}

	if prev != "" && next != "" && prev >= next {
		return "", fmt.Errorf("%w: %q is not below %q", ErrNotAscending, prev, next)
	}

	switch {
	case prev == "" && next == "":
		return firstKey, nil
	case prev == "":
		return before(nextInt, nextFrac)
	case next == "":
		return after(prevInt, prevFrac), nil
	case prevInt == nextInt:
		return prevInt + midpoint(prevFrac, nextFrac), nil
	}

	// The bounds sit on different integers. If the next integer up is still
	// below next, it is the shortest key that fits and costs no fraction at
	// all; that is the case that keeps a list of appends short.
	stepped, err := increment(prevInt)
	if err != nil {
		return "", fmt.Errorf("%w: nothing sorts after %q", err, prev)
	}

	if stepped < next {
		return stepped, nil
	}

	return prevInt + midpointAfter(prevFrac), nil
}

// before returns a key below the one that splits into integer and fraction.
func before(integer, fraction string) (string, error) {
	// The bottom integer cannot be stepped down, but its fraction can always
	// be subdivided.
	if integer == smallestInteger {
		return integer + midpoint("", fraction), nil
	}

	// A key with a fraction is preceded by its own integer part, which is
	// shorter than any fraction we could compute.
	if fraction != "" {
		return integer, nil
	}

	stepped, err := decrement(integer)
	if err != nil {
		return "", fmt.Errorf("%w: nothing sorts before %q", err, integer)
	}

	// Stepping down landed on the one string that is not a usable key. Its
	// fractions are, and they all sort below the bound we were given.
	if stepped == smallestInteger {
		return stepped + midpointAfter(""), nil
	}

	return stepped, nil
}

// after returns a key above the one that splits into integer and fraction.
// It cannot fail: an integer space that has run out at the top still has room
// for one more fraction under its last integer, and always will.
func after(integer, fraction string) string {
	stepped, err := increment(integer)
	if err != nil {
		return integer + midpointAfter(fraction)
	}

	return stepped
}

// splitBound is split, but accepts the empty string as "no bound".
func splitBound(key string) (integer, fraction string, err error) {
	if key == "" {
		return "", "", nil
	}

	return split(key)
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

// increment returns the next integer above the one given.
func increment(integer string) (string, error) {
	header := integer[0]
	body := []byte(integer[1:])

	carry := true
	for i := len(body) - 1; carry && i >= 0; i-- {
		next := strings.IndexByte(digits, body[i]) + 1
		if next == len(digits) {
			body[i] = digits[0]

			continue
		}

		body[i] = digits[next]
		carry = false
	}

	if !carry {
		return string(header) + string(body), nil
	}

	switch header {
	case 'Z':
		// The negative half ends exactly where the positive half begins.
		return firstKey, nil
	case 'z':
		return "", ErrExhausted
	}

	// Carrying out of the digits means the next integer needs a different
	// width: one digit more above 'a', one digit less below it.
	header++
	if header > 'a' {
		body = append(body, digits[0])
	} else {
		body = body[:len(body)-1]
	}

	return string(header) + string(body), nil
}

// decrement returns the next integer below the one given.
func decrement(integer string) (string, error) {
	header := integer[0]
	body := []byte(integer[1:])
	last := digits[len(digits)-1]

	borrow := true
	for i := len(body) - 1; borrow && i >= 0; i-- {
		next := strings.IndexByte(digits, body[i]) - 1
		if next < 0 {
			body[i] = last

			continue
		}

		body[i] = digits[next]
		borrow = false
	}

	if !borrow {
		return string(header) + string(body), nil
	}

	switch header {
	case 'a':
		return string([]byte{'Z', last}), nil
	case 'A':
		return "", ErrExhausted
	}

	header--
	if header < 'Z' {
		body = append(body, last)
	} else {
		body = body[:len(body)-1]
	}

	return string(header) + string(body), nil
}

// midpoint returns a fraction strictly between a and b. Both belong to the
// same integer part, a sorts below b, and neither ends in a zero digit.
func midpoint(a, b string) string {
	// Digits the two share decide nothing; subdivide what is left after them.
	if shared := commonPrefixLen(a, b); shared > 0 {
		return b[:shared] + midpoint(drop(a, shared), b[shared:])
	}

	low, high := leadingDigit(a), strings.IndexByte(digits, b[0])
	if high-low > 1 {
		return string(digits[(low+high+1)/2])
	}

	// The two leading digits are neighbors, so no digit fits between them.
	// When b has digits to spare, dropping them yields a fraction still above
	// a and below b.
	if len(b) > 1 {
		return b[:1]
	}

	// Otherwise keep a's leading digit and place the key after the rest of a,
	// which is by definition below b.
	return string(digits[low]) + midpointAfter(drop(a, 1))
}

// midpointAfter returns a fraction strictly above a that stays below every
// fraction starting with a higher digit — appending within one integer part.
func midpointAfter(a string) string {
	low := leadingDigit(a)
	if low < len(digits)-1 {
		return string(digits[(low+len(digits)+1)/2])
	}

	// a already leads with the highest digit, so the answer has to keep it and
	// grow one place to the right.
	return string(digits[low]) + midpointAfter(drop(a, 1))
}

// leadingDigit reads the first digit of a fraction. A missing one is a zero:
// "5" and "50" name the same position.
func leadingDigit(fraction string) int {
	if fraction == "" {
		return 0
	}

	return strings.IndexByte(digits, fraction[0])
}

// drop returns fraction without its leading n digits. A fraction that has run
// out stands for the zeros behind it, so dropping past its end yields the
// empty fraction rather than an error.
func drop(fraction string, n int) string {
	if n >= len(fraction) {
		return ""
	}

	return fraction[n:]
}

// commonPrefixLen counts the leading digits a and b agree on, reading a as
// zero-padded for the same reason leadingDigit does.
func commonPrefixLen(a, b string) int {
	shared := 0
	for shared < len(b) {
		digit := byte('0')
		if shared < len(a) {
			digit = a[shared]
		}

		if digit != b[shared] {
			break
		}

		shared++
	}

	return shared
}
