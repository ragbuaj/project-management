package authz

import (
	"context"
	"fmt"
)

// Memberships answers who belongs to what.
//
// It is declared here, where it is used, rather than beside an implementation
// — the same arrangement as httpx.Limiter and for the same reason (ADR-0008).
// project_members and folder_members belong to the project module, sqlc.yaml
// keeps one entry per module, and a package that reaches into another module's
// tables is the layering violation that file says it cannot catch. So authz
// states what it needs and the project module supplies it in Phase 1.
type Memberships interface {
	// InProject reports whether the user belongs to the project — directly,
	// or through the folder the project sits in.
	//
	// The inheritance is the implementation's job, not this package's: a
	// caller of authz should never have to know that folders exist, and an
	// implementation that answers this with one query is the reason it is
	// asked as one question (ADR-0011).
	InProject(ctx context.Context, userID, projectID string) (bool, error)

	// InFolder reports whether the user belongs to the folder itself.
	InFolder(ctx context.Context, userID, folderID string) (bool, error)
}

// Resolver answers the scope half of a decision, and combines it with the
// right half for the callers that want one answer.
type Resolver struct {
	memberships Memberships
}

func NewResolver(memberships Memberships) *Resolver {
	return &Resolver{memberships: memberships}
}

// InProjectScope reports whether this project is within the caller's reach.
//
// **This is the only place the owner's exception lives.** Written once, here,
// rather than as a condition inside every rule: an exception spread across a
// table is an exception that will one day be missed in one row, and in this
// layer one miss means somebody reads what was never theirs.
//
// docs/authorization.md requires that an owner reaching outside their own
// memberships leaves a record in activity_events. That record is not written
// here — this function decides, it does not act — so whoever calls it for an
// owner is responsible for the trail. The second return value says exactly
// when that applies.
func (r *Resolver) InProjectScope(ctx context.Context, caller Caller, projectID string) (inScope, viaOwner bool, err error) {
	if caller.Role == RoleOwner {
		return true, true, nil
	}

	member, err := r.memberships.InProject(ctx, caller.UserID, projectID)
	if err != nil {
		// false travels with the error for the same reason ParseRole returns
		// RoleNone with one: a caller who mishandles the failure must deny.
		return false, false, fmt.Errorf("read project membership: %w", err)
	}

	return member, false, nil
}

// InFolderScope is the same question about a folder.
func (r *Resolver) InFolderScope(ctx context.Context, caller Caller, folderID string) (inScope, viaOwner bool, err error) {
	if caller.Role == RoleOwner {
		return true, true, nil
	}

	member, err := r.memberships.InFolder(ctx, caller.UserID, folderID)
	if err != nil {
		return false, false, fmt.Errorf("read folder membership: %w", err)
	}

	return member, false, nil
}

// AllowOnProject is both halves at once, for the handlers that have nothing to
// do with the difference.
//
// Scope is checked first, and that order is deliberate: a caller who cannot
// see the project at all must be answered 404, while one who can see it but
// may not act gets 403 (docs/authorization.md). Asking the cheaper question
// first would make those two indistinguishable to the handler.
func (r *Resolver) AllowOnProject(ctx context.Context, caller Caller, action Action, projectID string) (allowed, viaOwner bool, err error) {
	inScope, viaOwner, err := r.InProjectScope(ctx, caller, projectID)
	if err != nil || !inScope {
		return false, false, err
	}

	return Can(caller, action), viaOwner, nil
}

// AllowOnFolder is the same for a folder.
func (r *Resolver) AllowOnFolder(ctx context.Context, caller Caller, action Action, folderID string) (allowed, viaOwner bool, err error) {
	inScope, viaOwner, err := r.InFolderScope(ctx, caller, folderID)
	if err != nil || !inScope {
		return false, false, err
	}

	return Can(caller, action), viaOwner, nil
}

// AllowMoveProjectToFolder is the one decision that needs scope on two sides at
// once, so it is spelled out rather than smuggled through AllowOnProject with
// whichever id the call site happened to have.
//
// Moving a project into a folder hands it to everyone in that folder
// (ADR-0011). That makes it an access change wearing the clothes of tidying
// up, and it is why both sides have to consent: reach over the project being
// moved, and reach over the folder receiving it.
func (r *Resolver) AllowMoveProjectToFolder(ctx context.Context, caller Caller, projectID, folderID string) (allowed, viaOwner bool, err error) {
	overProject, ownerOverProject, err := r.InProjectScope(ctx, caller, projectID)
	if err != nil || !overProject {
		return false, false, err
	}

	overFolder, ownerOverFolder, err := r.InFolderScope(ctx, caller, folderID)
	if err != nil || !overFolder {
		return false, false, err
	}

	return Can(caller, ActionProjectMoveToFolder), ownerOverProject || ownerOverFolder, nil
}
