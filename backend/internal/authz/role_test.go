package authz_test

import (
	"errors"
	"testing"

	"github.com/ragbuaj/project-management/backend/internal/authz"
)

// allRoles is every role this package can produce, weakest first. Spelled out
// rather than generated, so adding one to the type without deciding where it
// ranks shows up here as a test to update rather than as silence.
var allRoles = []authz.Role{authz.RoleNone, authz.RoleViewer, authz.RoleMember, authz.RoleAdmin}

// The three names are the ones the CHECK constraints on project_members and
// folder_members allow, and nothing else may cross into this package.
func TestParseRoleAcceptsExactlyWhatTheDatabaseStores(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]authz.Role{
		"viewer": authz.RoleViewer,
		"member": authz.RoleMember,
		"admin":  authz.RoleAdmin,
	} {
		got, err := authz.ParseRole(name)
		if err != nil {
			t.Errorf("ParseRole(%q): %v", name, err)
		}

		if got != want {
			t.Errorf("ParseRole(%q) = %v, want %v", name, got, want)
		}
	}
}

// Matching is exact. A spelling this application never writes is a value some
// other writer put there, and accepting it means accepting whatever else they
// wrote too.
func TestParseRoleRefusesAnythingElse(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "Admin", "ADMIN", " admin", "admin ", "owner", "guest", "share", "superuser"} {
		got, err := authz.ParseRole(name)
		if !errors.Is(err, authz.ErrUnknownRole) {
			t.Errorf("ParseRole(%q) err = %v, want ErrUnknownRole", name, err)
		}

		// The safe half must not depend on the caller remembering to check the
		// error: a mishandled failure has to deny, not grant.
		if got != authz.RoleNone {
			t.Errorf("ParseRole(%q) = %v alongside an error, want RoleNone", name, got)
		}
	}
}

// The zero value denies everything. Code that forgets to assign a role, or a
// struct field that was never filled, must fail closed rather than land on
// whichever role happens to be first.
func TestTheZeroValueIsTheWeakestRole(t *testing.T) {
	t.Parallel()

	var unset authz.Role

	if unset != authz.RoleNone {
		t.Fatalf("zero value = %v, want RoleNone", unset)
	}

	for _, r := range allRoles[1:] {
		if unset.AtLeast(r) {
			t.Errorf("an unassigned role satisfies %v", r)
		}
	}
}

// The order is the whole of the hierarchy, and every row in
// docs/authorization.md is written as "from this rank upward".
func TestTheRanksAreOrderedWeakestToStrongest(t *testing.T) {
	t.Parallel()

	for i, lower := range allRoles {
		for _, higher := range allRoles[i:] {
			if !higher.AtLeast(lower) {
				t.Errorf("%v does not satisfy %v, but ranks above it", higher, lower)
			}
		}

		for _, weaker := range allRoles[:i] {
			if weaker.AtLeast(lower) {
				t.Errorf("%v satisfies %v, but ranks below it", weaker, lower)
			}
		}
	}
}

// ADR-0011: the effective role is the higher of the two, so that a folder
// invitation can only ever add. Checked over every pair rather than a few
// examples, because "the higher one" is the kind of claim that is easy to
// write and easy to get backwards.
func TestMaxAlwaysReturnsTheStrongerOfThePair(t *testing.T) {
	t.Parallel()

	for _, a := range allRoles {
		for _, b := range allRoles {
			got := authz.Max(a, b)

			if !got.AtLeast(a) || !got.AtLeast(b) {
				t.Errorf("Max(%v, %v) = %v, which is weaker than one of them", a, b, got)
			}

			if got != a && got != b {
				t.Errorf("Max(%v, %v) = %v, which is neither of them", a, b, got)
			}

			if flipped := authz.Max(b, a); flipped != got {
				t.Errorf("Max(%v, %v) = %v but Max(%v, %v) = %v", a, b, got, b, a, flipped)
			}
		}
	}
}

// A folder membership must never take rights away — the case ADR-0011 rejects
// "most specific wins" for, written out as the situation it protects.
func TestAFolderRoleCannotDemoteAProjectAdmin(t *testing.T) {
	t.Parallel()

	if got := authz.Max(authz.RoleAdmin, authz.RoleViewer); got != authz.RoleAdmin {
		t.Errorf("a project admin invited to the folder as a viewer became %v", got)
	}
}

// The stored spelling has to survive the round trip, or a role read from one
// table and written to another would change meaning on the way.
func TestTheThreeStoredRolesSurviveARoundTrip(t *testing.T) {
	t.Parallel()

	for _, r := range allRoles[1:] {
		back, err := authz.ParseRole(r.String())
		if err != nil {
			t.Errorf("ParseRole(%q): %v", r.String(), err)

			continue
		}

		if back != r {
			t.Errorf("%v round-tripped to %v", r, back)
		}
	}
}

// RoleNone has a name so that a denial reads as one in a log or a failure
// message. It must not be a name the parser accepts back, because nothing
// stores it.
func TestRoleNoneIsPrintableButNotStorable(t *testing.T) {
	t.Parallel()

	if authz.RoleNone.String() != "none" {
		t.Errorf("RoleNone prints as %q", authz.RoleNone)
	}

	if _, err := authz.ParseRole("none"); !errors.Is(err, authz.ErrUnknownRole) {
		t.Error(`ParseRole("none") was accepted; no table stores that value`)
	}
}
