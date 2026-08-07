package authz_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ragbuaj/project-management/backend/internal/authz"
)

// fakeMemberships stands in for the project module's repository, which lands
// in Phase 1. It records what it was asked, because the arguments are two ids
// of the same type in a row — the shape where a swap compiles cleanly and then
// answers about the wrong pair.
type fakeMemberships struct {
	inProject bool
	inFolder  bool
	err       error

	gotUser     string
	gotResource string
	calls       int
}

func (f *fakeMemberships) InProject(_ context.Context, userID, projectID string) (bool, error) {
	f.calls++
	f.gotUser, f.gotResource = userID, projectID

	if f.err != nil {
		return false, f.err
	}

	return f.inProject, nil
}

func (f *fakeMemberships) InFolder(_ context.Context, userID, folderID string) (bool, error) {
	f.calls++
	f.gotUser, f.gotResource = userID, folderID

	if f.err != nil {
		return false, f.err
	}

	return f.inFolder, nil
}

func contributor() authz.Caller {
	return authz.Caller{UserID: "user-1", Role: authz.RoleContributor}
}

func owner() authz.Caller {
	return authz.Caller{UserID: "the-owner", Role: authz.RoleOwner}
}

// Membership is the scope half. Someone who belongs to neither the project nor
// its folder is out of reach of it entirely, whatever their rank.
func TestSomebodyWhoIsNotAMemberIsOutOfScope(t *testing.T) {
	t.Parallel()

	store := &fakeMemberships{inProject: false}

	inScope, viaOwner, err := authz.NewResolver(store).InProjectScope(t.Context(), contributor(), "project-1")
	if err != nil {
		t.Fatalf("InProjectScope(): %v", err)
	}

	if inScope {
		t.Error("a non-member was in scope")
	}

	if viaOwner {
		t.Error("viaOwner was set for somebody who is not the owner")
	}
}

func TestAMemberIsInScope(t *testing.T) {
	t.Parallel()

	store := &fakeMemberships{inProject: true}

	inScope, viaOwner, err := authz.NewResolver(store).InProjectScope(t.Context(), contributor(), "project-1")
	if err != nil {
		t.Fatalf("InProjectScope(): %v", err)
	}

	if !inScope {
		t.Error("a member was out of scope")
	}

	if viaOwner {
		t.Error("viaOwner was set for an ordinary member")
	}
}

// ADR-0012: the owner reaches every folder and project without being a member.
// The membership store must not even be consulted — asking and ignoring the
// answer would make the exception depend on the store being reachable.
func TestTheOwnerIsInScopeWithoutAskingAboutMembership(t *testing.T) {
	t.Parallel()

	store := &fakeMemberships{inProject: false, err: errors.New("the store must not be called")}

	inScope, viaOwner, err := authz.NewResolver(store).InProjectScope(t.Context(), owner(), "project-1")
	if err != nil {
		t.Fatalf("InProjectScope(): %v", err)
	}

	if !inScope {
		t.Error("the owner was out of scope")
	}

	if !viaOwner {
		t.Error("viaOwner was not set, so nothing downstream knows to record the access")
	}

	if store.calls != 0 {
		t.Errorf("the store was consulted %d times for an owner", store.calls)
	}
}

// docs/authorization.md requires that an owner reaching outside their own
// memberships leaves a record. This package does not write that record, so the
// only thing it can be held to is telling the truth about when it applies —
// and it must not claim it for an ordinary member who happens to be allowed.
func TestViaOwnerIsOnlySetWhenTheOwnerExceptionWasUsed(t *testing.T) {
	t.Parallel()

	store := &fakeMemberships{inProject: true, inFolder: true}
	resolver := authz.NewResolver(store)

	_, viaOwner, err := resolver.AllowOnProject(t.Context(), contributor(), authz.ActionProjectView, "project-1")
	if err != nil {
		t.Fatalf("AllowOnProject(): %v", err)
	}

	if viaOwner {
		t.Error("an ordinary member's access was marked as the owner exception")
	}
}

// A database that will not answer is not a caller without rights. The error
// has to travel so the request can be answered 500, and the scope has to be
// false so a caller who ignores the error still denies.
func TestAStoreFailureIsReportedAndStillDenies(t *testing.T) {
	t.Parallel()

	wanted := errors.New("connection refused")

	inScope, _, err := authz.NewResolver(&fakeMemberships{err: wanted}).
		InProjectScope(t.Context(), contributor(), "project-1")
	if !errors.Is(err, wanted) {
		t.Errorf("err = %v, want it to wrap the store's own error", err)
	}

	if inScope {
		t.Error("scope was granted alongside an error")
	}
}

// Two string ids of the same type, side by side. Swapping them compiles, and
// the result is a question answered about the wrong pair — which, with ids
// that are hard to tell apart by eye, would read as an unrelated bug.
func TestTheUserAndResourceIdsReachTheStoreInThatOrder(t *testing.T) {
	t.Parallel()

	store := &fakeMemberships{}

	if _, _, err := authz.NewResolver(store).InProjectScope(t.Context(), contributor(), "the-project"); err != nil {
		t.Fatalf("InProjectScope(): %v", err)
	}

	if store.gotUser != "user-1" || store.gotResource != "the-project" {
		t.Errorf("store was asked about user %q and resource %q", store.gotUser, store.gotResource)
	}
}

// Both halves have to hold. These two cases are the ones a single combined
// check would get wrong: reach without the rank, and the rank without reach.
func TestAllowNeedsBothScopeAndRank(t *testing.T) {
	t.Parallel()

	// In scope, but a contributor may not rename a project.
	allowed, _, err := authz.NewResolver(&fakeMemberships{inProject: true}).
		AllowOnProject(t.Context(), contributor(), authz.ActionProjectUpdate, "project-1")
	if err != nil {
		t.Fatalf("AllowOnProject(): %v", err)
	}

	if allowed {
		t.Error("a contributor renamed a project they belong to")
	}

	// The rank, but no membership.
	maintainer := authz.Caller{UserID: "user-2", Role: authz.RoleMaintainer}

	allowed, _, err = authz.NewResolver(&fakeMemberships{inProject: false}).
		AllowOnProject(t.Context(), maintainer, authz.ActionProjectUpdate, "project-1")
	if err != nil {
		t.Fatalf("AllowOnProject(): %v", err)
	}

	if allowed {
		t.Error("a maintainer acted on a project they do not belong to")
	}
}

// Pattern 6 of docs/authorization.md: moving a project into a folder hands it
// to everyone in that folder, so it is an access change wearing the clothes of
// tidying up. Both sides have to consent.
func TestMovingAProjectNeedsReachOverBothSides(t *testing.T) {
	t.Parallel()

	maintainer := authz.Caller{UserID: "user-2", Role: authz.RoleMaintainer}

	for _, tc := range []struct {
		name      string
		inProject bool
		inFolder  bool
		want      bool
	}{
		{"both", true, true, true},
		{"project only", true, false, false},
		{"folder only", false, true, false},
		{"neither", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeMemberships{inProject: tc.inProject, inFolder: tc.inFolder}

			allowed, _, err := authz.NewResolver(store).
				AllowMoveProjectToFolder(t.Context(), maintainer, "project-1", "folder-1")
			if err != nil {
				t.Fatalf("AllowMoveProjectToFolder(): %v", err)
			}

			if allowed != tc.want {
				t.Errorf("allowed = %v, want %v", allowed, tc.want)
			}
		})
	}
}

// A contributor who belongs to both still may not move a project: the rank is
// checked as well as the reach, and this is the case that proves the two
// scope lookups did not quietly become the whole decision.
func TestMovingAProjectStillNeedsTheRank(t *testing.T) {
	t.Parallel()

	store := &fakeMemberships{inProject: true, inFolder: true}

	allowed, _, err := authz.NewResolver(store).
		AllowMoveProjectToFolder(t.Context(), contributor(), "project-1", "folder-1")
	if err != nil {
		t.Fatalf("AllowMoveProjectToFolder(): %v", err)
	}

	if allowed {
		t.Error("a contributor moved a project between folders")
	}
}
