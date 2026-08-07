package authz_test

import (
	"slices"
	"testing"

	"github.com/ragbuaj/project-management/backend/internal/authz"
)

// who says, for one action, which callers the matrix in docs/authorization.md
// allows. It is written out here rather than derived from the rules table, so
// that this is a second reading of the document and not an echo of the first.
type who struct {
	// lowest is the weakest role that may act. RoleNone with none of the flags
	// below set means nobody may.
	lowest authz.Role
	// anyone marks an action open to every signed-in caller.
	anyone bool
	// alsoNeedsOwner marks an action the installation owner alone may take,
	// on top of lowest.
	alsoNeedsOwner bool
	// nobody marks an action refused to everyone, owner included.
	nobody bool
}

// matrix is docs/authorization.md, restated. Every action this package decides
// must appear; TestEveryActionIsCoveredByThisFile is what enforces that, so a
// new rule cannot arrive without somebody deciding who it is for.
var matrix = map[authz.Action]who{
	authz.ActionProjectCreate: {anyone: true},
	authz.ActionFolderCreate:  {anyone: true},

	authz.ActionProjectView:        {lowest: authz.RoleViewer},
	authz.ActionProjectMembersView: {lowest: authz.RoleViewer},
	authz.ActionFolderView:         {lowest: authz.RoleViewer},
	authz.ActionFolderMembersView:  {lowest: authz.RoleViewer},

	authz.ActionProjectUpdate:           {lowest: authz.RoleAdmin},
	authz.ActionProjectArchive:          {lowest: authz.RoleAdmin},
	authz.ActionProjectMembersManage:    {lowest: authz.RoleAdmin},
	authz.ActionProjectRemoveFromFolder: {lowest: authz.RoleAdmin},
	authz.ActionFolderRename:            {lowest: authz.RoleAdmin},
	authz.ActionFolderDelete:            {lowest: authz.RoleAdmin},
	authz.ActionFolderMembersManage:     {lowest: authz.RoleAdmin},

	authz.ActionProjectDelete:    {lowest: authz.RoleViewer, alsoNeedsOwner: true},
	authz.ActionProjectChangeKey: {nobody: true},
}

var everyRole = []authz.Role{authz.RoleNone, authz.RoleViewer, authz.RoleMember, authz.RoleAdmin}

// A rule that exists without anybody deciding who it is for is how a permission
// slips in unexamined. docs/authorization.md asks for a test per row; this is
// what makes the request impossible to forget.
func TestEveryActionIsCoveredByThisFile(t *testing.T) {
	t.Parallel()

	for _, action := range authz.Actions() {
		if _, covered := matrix[action]; !covered {
			t.Errorf("action %q has a rule but no expectation here", action)
		}
	}

	for action := range matrix {
		if !slices.Contains(authz.Actions(), action) {
			t.Errorf("action %q is expected here but has no rule", action)
		}
	}
}

// Patterns 1 and 3 of docs/authorization.md, over the whole table at once: the
// roles that may act are allowed, and every role below them is refused. Both
// halves matter — a rule that allows everybody passes the positive test alone.
func TestEachActionAllowsExactlyTheRolesTheMatrixSays(t *testing.T) {
	t.Parallel()

	for action, expected := range matrix {
		t.Run(string(action), func(t *testing.T) {
			t.Parallel()

			for _, role := range everyRole {
				for _, owner := range []bool{false, true} {
					caller := authz.Caller{UserID: "u", IsOwner: owner}

					want := allowedBy(expected, role, owner)
					if got := authz.Can(caller, action, role); got != want {
						t.Errorf("Can(owner=%v, %s, %v) = %v, want %v", owner, action, role, got, want)
					}
				}
			}
		})
	}
}

func allowedBy(expected who, role authz.Role, owner bool) bool {
	switch {
	case expected.nobody:
		return false
	case expected.anyone:
		return true
	case expected.alsoNeedsOwner && !owner:
		return false
	case expected.lowest == authz.RoleNone:
		return false
	default:
		return role.AtLeast(expected.lowest)
	}
}

// Pattern 2: somebody who belongs to neither the project nor its folder holds
// RoleNone, and must be refused everything that touches a resource. The two
// create actions are the exception by design — there is no resource yet to be
// a member of.
func TestAStrangerIsRefusedEverythingWithAResource(t *testing.T) {
	t.Parallel()

	caller := authz.Caller{UserID: "stranger"}

	for _, action := range authz.Actions() {
		if matrix[action].anyone {
			continue
		}

		if authz.Can(caller, action, authz.RoleNone) {
			t.Errorf("a non-member was allowed to %s", action)
		}
	}
}

// The same, for the installation owner. docs/authorization.md is explicit that
// owner is not a rank above admin: outside the projects they belong to, the
// owner may only do what the closed list names, and none of that is here yet.
func TestTheOwnerIsNotASuperuserOverProjectsTheyDoNotBelongTo(t *testing.T) {
	t.Parallel()

	owner := authz.Caller{UserID: "owner", IsOwner: true}

	for _, action := range authz.Actions() {
		if matrix[action].anyone {
			continue
		}

		if authz.Can(owner, action, authz.RoleNone) {
			t.Errorf("the owner was allowed to %s without belonging to it", action)
		}
	}
}

// An endpoint added without a rule must arrive refused. The omission then
// shows up as something that does not work, rather than as something that
// works for everybody.
func TestAnActionWithNoRuleIsRefused(t *testing.T) {
	t.Parallel()

	owner := authz.Caller{UserID: "owner", IsOwner: true}

	for _, action := range []authz.Action{"", "project.destroy", "card.create", "PROJECT.VIEW"} {
		if authz.Can(owner, action, authz.RoleAdmin) {
			t.Errorf("unknown action %q was allowed", action)
		}
	}
}

// The key is part of every card number ever written into a commit message, a
// chat, or a document. Changing it rewrites history that lives outside this
// database, which is why the answer is no to everyone.
func TestNobodyCanChangeAProjectKey(t *testing.T) {
	t.Parallel()

	for _, role := range everyRole {
		for _, owner := range []bool{false, true} {
			caller := authz.Caller{UserID: "u", IsOwner: owner}
			if authz.Can(caller, authz.ActionProjectChangeKey, role) {
				t.Errorf("owner=%v role=%v was allowed to change the key", owner, role)
			}
		}
	}
}

// Permanent deletion needs both halves: the installation owner, and membership
// of the project being deleted. An owner facing an abandoned project adds
// themselves to it first — a step that is recorded and that notifies its
// members, which is the difference between emergency access and quiet access.
func TestPermanentDeletionNeedsTheOwnerAndMembershipTogether(t *testing.T) {
	t.Parallel()

	owner := authz.Caller{UserID: "owner", IsOwner: true}
	member := authz.Caller{UserID: "member"}

	if authz.Can(owner, authz.ActionProjectDelete, authz.RoleNone) {
		t.Error("the owner deleted a project they do not belong to")
	}

	if authz.Can(member, authz.ActionProjectDelete, authz.RoleAdmin) {
		t.Error("a project admin who is not the installation owner deleted a project")
	}

	if !authz.Can(owner, authz.ActionProjectDelete, authz.RoleViewer) {
		t.Error("the owner could not delete a project they belong to")
	}
}

// Pattern 6: moving a project into a folder hands it to everyone in that
// folder, so it is an access change wearing the clothes of tidying up. Both
// sides have to consent.
func TestMovingAProjectNeedsAdminOnBothSides(t *testing.T) {
	t.Parallel()

	caller := authz.Caller{UserID: "u"}

	for _, tc := range []struct {
		project authz.Role
		folder  authz.Role
		want    bool
	}{
		{authz.RoleAdmin, authz.RoleAdmin, true},
		{authz.RoleAdmin, authz.RoleMember, false},
		{authz.RoleMember, authz.RoleAdmin, false},
		{authz.RoleAdmin, authz.RoleNone, false},
		{authz.RoleNone, authz.RoleAdmin, false},
		{authz.RoleNone, authz.RoleNone, false},
	} {
		if got := authz.CanMoveProjectToFolder(caller, tc.project, tc.folder); got != tc.want {
			t.Errorf("project %v + folder %v = %v, want %v", tc.project, tc.folder, got, tc.want)
		}
	}
}

// Pattern 5, through both halves of the package at once: a right that exists
// only because of a folder membership has to survive the trip from
// EffectiveRole into Can. Testing them apart would leave the join untested,
// and the join is the part ADR-0011 added.
func TestARightThatComesOnlyFromTheFolderReachesTheDecision(t *testing.T) {
	t.Parallel()

	resolver := authz.NewResolver(&fakeMemberships{folder: authz.RoleAdmin})

	role, err := resolver.EffectiveRole(t.Context(), "u", "p")
	if err != nil {
		t.Fatalf("EffectiveRole(): %v", err)
	}

	if !authz.Can(authz.Caller{UserID: "u"}, authz.ActionProjectUpdate, role) {
		t.Error("a folder admin could not act on a project inside that folder")
	}
}
