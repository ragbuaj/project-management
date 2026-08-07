package fracdex_test

import (
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/ragbuaj/project-management/backend/internal/fracdex"
)

// The property ADR-0003 asked for: whatever pair of neighbors a drag lands
// between, the new key sorts between them. Three thousand insertions at
// random positions, each one checked against its bounds and against the whole
// list afterwards.
func TestEveryInsertionLandsBetweenItsNeighbors(t *testing.T) {
	t.Parallel()

	// Fixed seed: a failing run has to be reproducible from the failure alone.
	rng := rand.New(rand.NewPCG(0x5eed, 0xf00d))

	var keys []string
	for i := range 3000 {
		at := rng.IntN(len(keys) + 1)

		var prev, next string
		if at > 0 {
			prev = keys[at-1]
		}

		if at < len(keys) {
			next = keys[at]
		}

		key, err := fracdex.Between(prev, next)
		if err != nil {
			t.Fatalf("insertion %d between %q and %q: %v", i, prev, next, err)
		}

		if err := fracdex.Validate(key); err != nil {
			t.Fatalf("insertion %d produced %q, which does not validate: %v", i, key, err)
		}

		if prev != "" && key <= prev {
			t.Fatalf("insertion %d produced %q, which does not sort above %q", i, key, prev)
		}

		if next != "" && key >= next {
			t.Fatalf("insertion %d produced %q, which does not sort below %q", i, key, next)
		}

		keys = slices.Insert(keys, at, key)
	}

	if !slices.IsSorted(keys) {
		t.Error("the list is no longer sorted after 3000 insertions")
	}

	if len(slices.Compact(slices.Clone(keys))) != len(keys) {
		t.Error("two insertions produced the same key")
	}
}

// Appending is the common case — every card created lands at one end of a
// list — and it is the case the integer part exists for. A thousand appends
// must not produce keys a thousand bytes long.
func TestAppendingDoesNotLengthenKeys(t *testing.T) {
	t.Parallel()

	const appends = 1000

	keys := make([]string, 0, appends)
	last := ""

	for i := range appends {
		key, err := fracdex.Between(last, "")
		if err != nil {
			t.Fatalf("append %d after %q: %v", i, last, err)
		}

		keys = append(keys, key)
		last = key
	}

	if !slices.IsSorted(keys) {
		t.Fatal("appended keys do not sort in the order they were created")
	}

	longest := 0
	for _, key := range keys {
		longest = max(longest, len(key))
	}

	// 62 keys fit in two bytes, 62*62 in three. Anything longer means the
	// integer part stopped carrying and fractions took over.
	if longest > 3 {
		t.Errorf("longest key after %d appends is %d bytes, want at most 3", appends, longest)
	}
}

// Repeated insertion at the same point is what the rebalance job in ADR-0003
// exists for: the keys stay correct but grow, and the ADR schedules a rewrite
// once a scope passes 40 bytes. This pins both halves of that claim — the
// order survives, and the threshold is reachable by ordinary use.
func TestRepeatedInsertionAtOnePointStaysOrderedAsKeysGrow(t *testing.T) {
	t.Parallel()

	const insertions = 300

	lower, upper := "a0", "a1"
	longest := 0

	for i := range insertions {
		key, err := fracdex.Between(lower, upper)
		if err != nil {
			t.Fatalf("insertion %d between %q and %q: %v", i, lower, upper, err)
		}

		if key <= lower || key >= upper {
			t.Fatalf("insertion %d produced %q, outside (%q, %q)", i, key, lower, upper)
		}

		if err := fracdex.Validate(key); err != nil {
			t.Fatalf("insertion %d produced %q, which does not validate: %v", i, key, err)
		}

		longest = max(longest, len(key))
		upper = key
	}

	const rebalanceThreshold = 40
	if longest <= rebalanceThreshold {
		t.Errorf("longest key after %d insertions at one point is %d bytes; "+
			"the rebalance threshold of %d was never reached, so this test no "+
			"longer covers what it claims", insertions, longest, rebalanceThreshold)
	}
}

// Positions arrive from clients as opaque strings, so Between is reachable
// with anything a request body can carry. Whatever it accepts, it must place
// between the bounds it was given.
func FuzzBetweenStaysWithinItsBounds(f *testing.F) {
	seeds := [][2]string{
		{"", ""}, {"", "a0"}, {"a0", ""}, {"a0", "a1"}, {"a0V", "a1"},
		{"Zz", "a0"}, {"b125", "b129"}, {"a0", "a0V"}, {"zzz", ""},
		{"A000000000000000000000000001", ""},
	}
	for _, seed := range seeds {
		f.Add(seed[0], seed[1])
	}

	f.Fuzz(func(t *testing.T, prev, next string) {
		key, err := fracdex.Between(prev, next)
		if err != nil {
			return
		}

		if err := fracdex.Validate(key); err != nil {
			t.Errorf("Between(%q, %q) = %q, which does not validate: %v", prev, next, key, err)
		}

		if prev != "" && key <= prev {
			t.Errorf("Between(%q, %q) = %q, which does not sort above the lower bound", prev, next, key)
		}

		if next != "" && key >= next {
			t.Errorf("Between(%q, %q) = %q, which does not sort below the upper bound", prev, next, key)
		}
	})
}
