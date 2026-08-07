package authz_test

import (
	"slices"
	"testing"

	"github.com/ragbuaj/project-management/backend/internal/authz"
)

// lowestAllowed says, for one action, the weakest account role the matrix in
// docs/authorization.md permits. It is written out here rather than derived
// from the rules table, so that this is a second reading of the document and
// not an echo of the first.
//
// RoleNone means nobody may — the only such row today is project.change_key.
var lowestAllowed = map[authz.Action]authz.Role{
	authz.ActionProjectCreate: authz.RoleMaintainer,
	authz.ActionFolderCreate:  authz.RoleMaintainer,

	authz.ActionProjectView:        authz.RoleViewer,
	authz.ActionProjectMembersView: authz.RoleViewer,
	authz.ActionFolderView:         authz.RoleViewer,
	authz.ActionFolderMembersView:  authz.RoleViewer,

	authz.ActionProjectUpdate:        authz.RoleMaintainer,
	authz.ActionProjectArchive:       authz.RoleMaintainer,
	authz.ActionProjectMembersManage: authz.RoleMaintainer,
	authz.ActionProjectMoveToFolder:  authz.RoleMaintainer,
	authz.ActionFolderRename:         authz.RoleMaintainer,
	authz.ActionFolderDelete:         authz.RoleMaintainer,
	authz.ActionFolderMembersManage:  authz.RoleMaintainer,

	authz.ActionProjectDelete:    authz.RoleOwner,
	authz.ActionProjectChangeKey: authz.RoleNone,
}

// A rule that exists without anybody deciding who it is for is how a
// permission slips in unexamined. docs/authorization.md asks for a test per
// row; this is what makes the request impossible to forget.
func TestEveryActionIsCoveredByThisFile(t *testing.T) {
	t.Parallel()

	for _, action := range authz.Actions() {
		if _, covered := lowestAllowed[action]; !covered {
			t.Errorf("action %q has a rule but no expectation here", action)
		}
	}

	for action := range lowestAllowed {
		if !slices.Contains(authz.Actions(), action) {
			t.Errorf("action %q is expected here but has no rule", action)
		}
	}
}

// Patterns 1 and 3 of docs/authorization.md, over the whole table at once: the
// roles that may act are allowed, and every role below them is refused. Both
// halves matter — a rule that allows everybody passes the positive test alone.
func TestEachActionAllowsExactlyTheRanksTheMatrixSays(t *testing.T) {
	t.Parallel()

	for action, lowest := range lowestAllowed {
		t.Run(string(action), func(t *testing.T) {
			t.Parallel()

			for _, role := range allRoles {
				// RoleNone as the expectation means nobody, so no rank
				// satisfies it — including owner.
				want := lowest != authz.RoleNone && role.AtLeast(lowest)

				if got := authz.Can(authz.Caller{UserID: "u", Role: role}, action); got != want {
					t.Errorf("Can(%v, %s) = %v, want %v", role, action, got, want)
				}
			}
		})
	}
}

// A caller whose role never got assigned must be refused everything. This is
// the shape of a bug rather than of an attack — a Caller built without its
// role — and it has to fail closed.
func TestACallerWithNoRoleIsRefusedEverything(t *testing.T) {
	t.Parallel()

	for _, action := range authz.Actions() {
		if authz.Can(authz.Caller{UserID: "u"}, action) {
			t.Errorf("a caller with no role was allowed to %s", action)
		}
	}
}

// An endpoint added without a rule must arrive refused. The omission then
// shows up as something that does not work, rather than as something that
// works for everybody.
func TestAnActionWithNoRuleIsRefused(t *testing.T) {
	t.Parallel()

	owner := authz.Caller{UserID: "owner", Role: authz.RoleOwner}

	for _, action := range []authz.Action{"", "project.destroy", "card.create", "PROJECT.VIEW"} {
		if authz.Can(owner, action) {
			t.Errorf("unknown action %q was allowed", action)
		}
	}
}

// The key is part of every card number ever written into a commit message, a
// chat, or a document. Changing it rewrites history that lives outside this
// database, which is why the answer is no to everyone — the owner included,
// who is otherwise allowed everything.
func TestNobodyCanChangeAProjectKeyNotEvenTheOwner(t *testing.T) {
	t.Parallel()

	for _, role := range allRoles {
		if authz.Can(authz.Caller{UserID: "u", Role: role}, authz.ActionProjectChangeKey) {
			t.Errorf("%v was allowed to change the key", role)
		}
	}
}

// Permanent deletion is the owner's alone. A maintainer runs everything else
// about a project and still may not do this, which is the whole point of
// giving it its own rank rather than folding it in with the rest.
func TestPermanentDeletionIsTheOwnersAlone(t *testing.T) {
	t.Parallel()

	if !authz.Can(authz.Caller{UserID: "o", Role: authz.RoleOwner}, authz.ActionProjectDelete) {
		t.Error("the owner could not delete a project")
	}

	for _, role := range []authz.Role{authz.RoleViewer, authz.RoleContributor, authz.RoleMaintainer} {
		if authz.Can(authz.Caller{UserID: "u", Role: role}, authz.ActionProjectDelete) {
			t.Errorf("%v was allowed to delete a project permanently", role)
		}
	}
}

// Only maintainer and owner may create (ADR-0012). This is the rank that
// decides whether a new employee can open workspaces at all, so it is worth
// stating on its own rather than leaving it inside the table above.
func TestOnlyMaintainersAndTheOwnerMayCreate(t *testing.T) {
	t.Parallel()

	for _, action := range []authz.Action{authz.ActionProjectCreate, authz.ActionFolderCreate} {
		for _, role := range []authz.Role{authz.RoleNone, authz.RoleViewer, authz.RoleContributor} {
			if authz.Can(authz.Caller{UserID: "u", Role: role}, action) {
				t.Errorf("%v was allowed to %s", role, action)
			}
		}

		for _, role := range []authz.Role{authz.RoleMaintainer, authz.RoleOwner} {
			if !authz.Can(authz.Caller{UserID: "u", Role: role}, action) {
				t.Errorf("%v was refused %s", role, action)
			}
		}
	}
}
