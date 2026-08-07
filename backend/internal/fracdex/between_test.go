package fracdex_test

import (
	"errors"
	"testing"

	"github.com/ragbuaj/project-management/backend/internal/fracdex"
)

// The web client computes the key for a drag before the server answers, and
// the two must agree byte for byte or the card jumps when the response lands.
// These are the keys the fractional-indexing package produces, so a change
// here that keeps every ordering property intact but shifts the arithmetic
// still fails.
func TestBetweenComputesTheSameKeysAsTheWebClient(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		prev string
		next string
		want string
	}{
		{"an empty list starts in the middle of the space", "", "", "a0"},
		{"appending steps the integer up", "a0", "", "a1"},
		{"appending carries into a wider integer", "bzz", "", "c000"},
		{"prepending steps the integer down", "", "a0", "Zz"},
		{"prepending below zero keeps stepping", "", "Zz", "Zy"},
		{"prepending borrows into a wider integer", "", "Y00", "Xzzz"},
		{"a gap of one integer is filled by the integer between", "a0", "a2", "a1"},
		{"a gap of one integer ignores the upper fraction", "a0", "a1V", "a1"},
		{"neighboring integers grow a fraction", "a0", "a1", "a0V"},
		{"neighboring integers grow a fraction, higher up", "a1", "a2", "a1V"},
		{"a fraction is halved", "a0", "a0V", "a0G"},
		{"a fraction is halved again", "a0", "a0G", "a08"},
		{"a shared fraction prefix is kept", "b125", "b129", "b127"},
		{"a fraction shorter than the prefix it shares", "a0", "a00V", "a00G"},
		{"a fraction that runs out mid-comparison", "a0V", "a0V01", "a0V00V"},
		{"appending past a fraction returns to the integer", "a0V", "a1", "a0l"},
		{"the two halves of the space meet", "Zz", "a0", "ZzV"},
		{"crossing from the negative half costs nothing", "Zz", "a1", "a0"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, err := fracdex.Between(c.prev, c.next)
			if err != nil {
				t.Fatalf("Between(%q, %q): %v", c.prev, c.next, err)
			}

			if got != c.want {
				t.Errorf("Between(%q, %q) = %q, want %q", c.prev, c.next, got, c.want)
			}
		})
	}
}

// Nothing sorts strictly between a key and itself, and nothing sorts between
// two bounds handed over the wrong way round. Both mean the caller read the
// list wrongly, which is worth an error rather than an arbitrary key.
func TestBetweenRefusesBoundsThatDoNotAscend(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		prev string
		next string
		want error
	}{
		{"the same key twice", "a0", "a0", fracdex.ErrNotAscending},
		{"bounds the wrong way round", "a1", "a0", fracdex.ErrNotAscending},
		{"a prefix that is also the wrong way round", "a0V", "a0", fracdex.ErrNotAscending},
		{"a malformed lower bound", "a0!", "", fracdex.ErrInvalidKey},
		{"a malformed upper bound", "", "a0V0", fracdex.ErrInvalidKey},
		{"a lower bound that is the floor of the key space", "A00000000000000000000000000", "", fracdex.ErrInvalidKey},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, err := fracdex.Between(c.prev, c.next)
			if !errors.Is(err, c.want) {
				t.Fatalf("Between(%q, %q) = %q, %v; want %v", c.prev, c.next, got, err, c.want)
			}
		})
	}
}
