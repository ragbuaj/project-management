package fracdex_test

import (
	"errors"
	"testing"

	"github.com/ragbuaj/project-management/backend/internal/fracdex"
)

// The shapes a key is allowed to take. Each one is a case the generator will
// produce, so a validator that rejects any of them would reject its own
// output.
func TestValidateAcceptsEveryShapeOfKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		key  string
	}{
		{"the first key of a list", "a0"},
		{"an integer with no fraction", "a1"},
		{"a wider integer", "b00"},
		{"the widest integer", "z0000000000000000000000000z"},
		{"an integer below zero", "Zz"},
		{"a wider integer below zero", "Y00"},
		{"the narrowest integer above the floor", "A00000000000000000000000001"},
		{"an integer with a fraction", "a0V"},
		{"a long fraction", "a0VVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVV1"},
		{"a fraction whose zeros are not last", "a0V01"},
		{"an integer part ending in zero", "b10"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if err := fracdex.Validate(c.key); err != nil {
				t.Errorf("Validate(%q) = %v, want no error", c.key, err)
			}
		})
	}
}

// A key that is stored once sorts wrongly forever, so every way of spelling
// one wrongly has to be refused at the door rather than tolerated. Positions
// reach this package from request bodies, where any string is possible.
func TestValidateRefusesKeysThisPackageCouldNotHaveProduced(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		key  string
	}{
		{"the empty string", ""},
		{"a header with no digits behind it", "a"},
		{"an integer shorter than its header claims", "b0"},
		{"a wide integer one digit short", "z000000000000000000000000"},
		{"a byte outside base62", "a0!"},
		{"a header outside base62", "!0"},
		{"a multi-byte rune", "a0é"},
		{"a fraction ending in a zero digit", "a00"},
		{"a fraction that is only a zero digit", "b000"},
		{"a long fraction ending in a zero digit", "a0V0"},
		{"the floor of the key space", "A00000000000000000000000000"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if err := fracdex.Validate(c.key); !errors.Is(err, fracdex.ErrInvalidKey) {
				t.Errorf("Validate(%q) = %v, want ErrInvalidKey", c.key, err)
			}
		})
	}
}

// The rejection names the key it rejected. Without it the caller logs "order
// key is malformed" and has no way to tell which row carried it.
func TestTheRejectionNamesTheKeyItRejected(t *testing.T) {
	t.Parallel()

	err := fracdex.Validate("a0!")
	if !errors.Is(err, fracdex.ErrInvalidKey) {
		t.Fatalf("Validate(\"a0!\") = %v, want ErrInvalidKey", err)
	}

	if err.Error() == fracdex.ErrInvalidKey.Error() {
		t.Errorf("Validate(\"a0!\") = %q, which says nothing about the key", err)
	}
}
