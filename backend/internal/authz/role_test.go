package authz_test

import (
	"errors"
	"testing"

	"github.com/ragbuaj/project-management/backend/internal/authz"
)

// allRoles is every role this package can produce, weakest first. Spelled out
// rather than generated, so adding one to the type without deciding where it
// ranks shows up here as a test to update rather than as silence.
var allRoles = []authz.Role{
	authz.RoleNone,
	authz.RoleViewer,
	authz.RoleContributor,
	authz.RoleMaintainer,
	authz.RoleOwner,
}

// The four names are the ones the CHECK constraint on users.role allows, and
// nothing else may cross into this package.
func TestParseRoleAcceptsExactlyWhatTheDatabaseStores(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]authz.Role{
		"viewer":      authz.RoleViewer,
		"contributor": authz.RoleContributor,
		"maintainer":  authz.RoleMaintainer,
		"owner":       authz.RoleOwner,
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
// wrote too. The first three below are the vocabularies this project has
// already retired, which is exactly the kind of value that turns up in an old
// script.
func TestParseRoleRefusesAnythingElse(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"admin", "member", "project_manager", "", "Owner", "OWNER", " owner", "owner ", "guest", "share"} {
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
// Caller built without one, must fail closed rather than land on whichever
// role happens to be first.
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

// owner outranks everything. It is stated on its own because the rest of the
// package leans on it: Resolver skips the membership check for exactly this
// rank, and nothing above it exists to catch a mistake.
func TestOwnerIsTheHighestRank(t *testing.T) {
	t.Parallel()

	for _, r := range allRoles {
		if !authz.RoleOwner.AtLeast(r) {
			t.Errorf("owner does not satisfy %v", r)
		}

		if r != authz.RoleOwner && r.AtLeast(authz.RoleOwner) {
			t.Errorf("%v satisfies owner", r)
		}
	}
}

// The stored spelling has to survive the round trip, or a role read from the
// database and written back would change meaning on the way.
func TestTheFourStoredRolesSurviveARoundTrip(t *testing.T) {
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
