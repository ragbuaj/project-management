// Package authz decides permissions.
//
// docs/authorization.md §4 makes this the only place that decides: a handler
// asks and obeys, it never judges for itself. The reason is that authorization
// bugs are invisible — nothing crashes, nothing looks wrong, somebody simply
// reads what was never theirs — so the check has to live somewhere it can be
// read in one sitting and tested as a table.
//
// Every decision has two halves (ADR-0012), and they are kept apart on
// purpose:
//
//	Scope — is the caller a member of this folder or project?
//	Right — does the caller's account role permit this action?
//
// Both must hold. Can answers the second on its own and needs nothing but
// memory; the first needs the database and lives in scope.go.
package authz

import (
	"errors"
	"fmt"
)

// Role is an account role: what someone may do, wherever they are a member.
//
// It comes from users.role and nowhere else (ADR-0012). Membership carries no
// role — a person brings the same one to every folder and project that adds
// them.
//
// Two names from docs/authorization.md are deliberately absent. `guest` is
// nobody: a request without a live session is refused by RequireSession before
// authz is consulted. `share` never reaches an ordinary endpoint; it is served
// by a separate /api/v1/public/* path that returns already-trimmed data, which
// is what stops it from being an ordinary caller with an exception attached.
type Role uint8

const (
	// RoleNone is the zero value on purpose: a Role that was never assigned
	// denies everything, and code that forgets to set one fails closed.
	RoleNone Role = iota
	RoleViewer
	RoleContributor
	RoleMaintainer
	RoleOwner
)

// The numbers above are the hierarchy, and they are never stored anywhere.
// PostgreSQL keeps the text (see the CHECK on users.role), and ParseRole is
// the only door between the two. That is what makes it safe to renumber if a
// role ever has to be slotted in between two others.

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
// Matching is exact. 'Owner' is not a role this application writes, and
// accepting it would mean accepting whatever else a careless writer put there.
func ParseRole(name string) (Role, error) {
	switch name {
	case "viewer":
		return RoleViewer, nil
	case "contributor":
		return RoleContributor, nil
	case "maintainer":
		return RoleMaintainer, nil
	case "owner":
		return RoleOwner, nil
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
	case RoleContributor:
		return "contributor"
	case RoleMaintainer:
		return "maintainer"
	case RoleOwner:
		return "owner"
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

// Caller is who is asking.
//
// The role travels with the caller rather than being looked up per resource,
// which is the whole of ADR-0012: it is the same in every folder and project.
// What changes from one project to the next is only whether the caller is a
// member of it, and that is scope.go's question.
type Caller struct {
	UserID string
	Role   Role
}
