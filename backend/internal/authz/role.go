// Package authz decides permissions.
//
// docs/authorization.md §4 makes this the only place that decides: a handler
// asks and obeys, it never judges for itself. The reason is that authorization
// bugs are invisible — nothing crashes, nothing looks wrong, somebody simply
// reads what was never theirs — so the check has to live somewhere it can be
// read in one sitting and tested as a table.
package authz

import (
	"errors"
	"fmt"
)

// Role is what someone may do inside a project.
//
// Three of the six names in docs/authorization.md are deliberately absent:
//
//   - `guest` is nobody. A request without a live session is refused by
//     RequireSession before authz is ever consulted, so a Role for it would
//     only be a value nothing can hold.
//   - `share` never reaches an ordinary endpoint. It is served by a separate
//     /api/v1/public/* path that returns already-trimmed data, which is what
//     stops it from being an ordinary caller with an exception attached.
//   - `owner` is a property of the account, not a role in a project. Per
//     docs/authorization.md the installation owner has no privileges over the
//     contents of a project they are not a member of, so folding it in here
//     would put a superuser inside the one type that is supposed to say there
//     is none.
type Role uint8

const (
	// RoleNone is the zero value on purpose: a Role that was never assigned
	// denies everything, and code that forgets to set one fails closed.
	RoleNone Role = iota
	RoleViewer
	RoleMember
	RoleAdmin
)

// The numbers above are the hierarchy, and they are never stored anywhere.
// PostgreSQL keeps the text ('admin', 'member', 'viewer' — see the CHECK on
// project_members and folder_members), and ParseRole is the only door between
// the two. That is what makes it safe to renumber if a role ever has to be
// slotted in between two others.

// ErrUnknownRole means a role name arrived that this application does not
// issue. It is an error rather than a silent RoleNone because the two causes
// are very different: a typo in a query is a bug to fix, while a value written
// by something outside this application is a reason to look at the database.
var ErrUnknownRole = errors.New("unknown role")

// ParseRole turns the stored text into a Role.
//
// The returned Role is RoleNone whenever the error is non-nil, so a caller
// that mishandles the error still ends up denying rather than allowing. The
// safe half should not depend on anybody remembering to check.
//
// Matching is exact. 'Admin' is not a role this application writes, and
// accepting it would mean accepting whatever else a careless writer put there.
func ParseRole(name string) (Role, error) {
	switch name {
	case "viewer":
		return RoleViewer, nil
	case "member":
		return RoleMember, nil
	case "admin":
		return RoleAdmin, nil
	default:
		return RoleNone, fmt.Errorf("%w: %q", ErrUnknownRole, name)
	}
}

// String is the stored spelling, so a Role can go back where it came from and
// into a log line without a second mapping to keep in step.
func (r Role) String() string {
	switch r {
	case RoleViewer:
		return "viewer"
	case RoleMember:
		return "member"
	case RoleAdmin:
		return "admin"
	case RoleNone:
		return "none"
	default:
		// Unreachable through ParseRole. Named rather than left blank so that
		// a Role built by some future arithmetic shows up in a failure message
		// as itself instead of as an empty string.
		return fmt.Sprintf("role(%d)", uint8(r))
	}
}

// AtLeast reports whether r carries at least the rights of min.
//
// Every permission question in docs/authorization.md is shaped this way — a
// row grants from some rank upward — so this is the comparison the table in
// can.go is written against, rather than each site spelling out its own >=.
func (r Role) AtLeast(min Role) bool {
	return r >= min
}

// Max is the rule ADR-0011 turns on: an effective role is the higher of the
// role held on the project and the role held on its folder.
//
// The higher, not the more specific. A folder invitation is meant to grant
// access; if the lower rank won, adding somebody to a folder as a viewer would
// quietly strip the admin rights they already held on a project inside it —
// a revocation nobody asked for and no screen would explain.
func Max(a, b Role) Role {
	if a > b {
		return a
	}

	return b
}
