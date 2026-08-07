package authz

import (
	"context"
	"fmt"
)

// Memberships reads the two tables a project role can come from.
//
// It is declared here, where it is used, rather than beside an implementation
// — the same arrangement as httpx.Limiter and for the same reason (ADR-0008).
// project_members and folder_members belong to the project module, sqlc.yaml
// keeps one entry per module, and a package that reaches into another module's
// tables is the layering violation that file says it cannot catch. So authz
// states what it needs and the project module supplies it in Phase 1.
//
// The signature returns this package's own type rather than rows or strings:
// whoever implements it has to go through ParseRole, which is the only door
// between the stored text and the ranks used here.
type Memberships interface {
	// RolesOn reports the role the user holds on the project itself, and the
	// role they hold on the folder that project sits in.
	//
	// Either may be RoleNone, and both usually are. Someone who belongs to
	// neither is an ordinary answer, not an error — it is what every request
	// from a stranger looks like, and treating it as a failure would turn the
	// most common case into a log line.
	RolesOn(ctx context.Context, userID, projectID string) (project, folder Role, err error)
}

// Resolver answers what a person may do inside a project.
type Resolver struct {
	memberships Memberships
}

func NewResolver(memberships Memberships) *Resolver {
	return &Resolver{memberships: memberships}
}

// EffectiveRole is the one place ADR-0011's inheritance lives.
//
// Everything else in the application — handlers, services, the permission
// table in can.go — is written as though a person simply has a role in a
// project. This function is what makes that true, and keeping it to one
// function is the point: two sources of truth combined in several places will
// eventually be combined differently in one of them.
//
// A stranger gets RoleNone and no error. What the caller does with that is
// docs/authorization.md's business: a resource the caller may not see is
// answered 404 rather than 403, so that its existence does not leak.
func (r *Resolver) EffectiveRole(ctx context.Context, userID, projectID string) (Role, error) {
	project, folder, err := r.memberships.RolesOn(ctx, userID, projectID)
	if err != nil {
		// RoleNone travels with the error for the same reason ParseRole does
		// it: a caller who mishandles the failure must end up denying.
		return RoleNone, fmt.Errorf("read memberships for the project: %w", err)
	}

	return Max(project, folder), nil
}
