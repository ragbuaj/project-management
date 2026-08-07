package authz_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ragbuaj/project-management/backend/internal/authz"
)

// fakeMemberships stands in for the project module's repository, which lands
// in Phase 1. It also records what it was asked, because the arguments are two
// ids of the same type in a row — the shape where a swap compiles cleanly and
// then grants somebody the wrong project.
type fakeMemberships struct {
	project authz.Role
	folder  authz.Role
	err     error

	gotUser    string
	gotProject string
	calls      int
}

func (f *fakeMemberships) RolesOn(_ context.Context, userID, projectID string) (authz.Role, authz.Role, error) {
	f.calls++
	f.gotUser = userID
	f.gotProject = projectID

	if f.err != nil {
		return authz.RoleNone, authz.RoleNone, f.err
	}

	return f.project, f.folder, nil
}

func resolve(t *testing.T, store *fakeMemberships) (authz.Role, error) {
	t.Helper()

	return authz.NewResolver(store).EffectiveRole(t.Context(), "user-1", "project-1")
}

// A request from somebody who belongs to neither the project nor its folder is
// the most common request there is. Reporting it as a failure would turn every
// stranger into a log line and every log into noise.
func TestBelongingToNeitherIsAnAnswerNotAnError(t *testing.T) {
	t.Parallel()

	got, err := resolve(t, &fakeMemberships{})
	if err != nil {
		t.Fatalf("EffectiveRole(): %v", err)
	}

	if got != authz.RoleNone {
		t.Errorf("role = %v, want RoleNone", got)
	}
}

func TestAProjectRoleAloneIsTheEffectiveRole(t *testing.T) {
	t.Parallel()

	got, err := resolve(t, &fakeMemberships{project: authz.RoleMember})
	if err != nil {
		t.Fatalf("EffectiveRole(): %v", err)
	}

	if got != authz.RoleMember {
		t.Errorf("role = %v, want member", got)
	}
}

// The whole reason ADR-0011 was expensive: a right can arrive without any row
// in project_members at all. This is the case that did not exist before
// folders, and the one every permission test has to cover from now on.
func TestAFolderRoleAloneGrantsAccessToTheProject(t *testing.T) {
	t.Parallel()

	got, err := resolve(t, &fakeMemberships{folder: authz.RoleAdmin})
	if err != nil {
		t.Fatalf("EffectiveRole(): %v", err)
	}

	if got != authz.RoleAdmin {
		t.Errorf("role = %v, want admin — a folder admin is an admin of every project inside it", got)
	}
}

// The higher of the two, from either direction. Written as a table because the
// interesting half is the asymmetric pair: a folder viewer must not drag a
// project admin down, and a project viewer must not cap a folder admin.
func TestTheHigherOfTheTwoRolesWins(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		project authz.Role
		folder  authz.Role
		want    authz.Role
	}{
		{"folder outranks project", authz.RoleViewer, authz.RoleAdmin, authz.RoleAdmin},
		{"project outranks folder", authz.RoleAdmin, authz.RoleViewer, authz.RoleAdmin},
		{"equal ranks", authz.RoleMember, authz.RoleMember, authz.RoleMember},
		{"folder only", authz.RoleNone, authz.RoleMember, authz.RoleMember},
		{"project only", authz.RoleMember, authz.RoleNone, authz.RoleMember},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := resolve(t, &fakeMemberships{project: tc.project, folder: tc.folder})
			if err != nil {
				t.Fatalf("EffectiveRole(): %v", err)
			}

			if got != tc.want {
				t.Errorf("project %v + folder %v = %v, want %v", tc.project, tc.folder, got, tc.want)
			}
		})
	}
}

// A database that will not answer is not a caller without rights. The error
// has to travel so the request can be answered 500, and the role has to be
// RoleNone so that a caller who ignores the error still denies.
func TestAStoreFailureIsReportedAndStillDenies(t *testing.T) {
	t.Parallel()

	wanted := errors.New("connection refused")

	got, err := resolve(t, &fakeMemberships{project: authz.RoleAdmin, err: wanted})
	if !errors.Is(err, wanted) {
		t.Errorf("err = %v, want it to wrap the store's own error", err)
	}

	if got != authz.RoleNone {
		t.Errorf("role = %v alongside an error, want RoleNone", got)
	}
}

// Two string ids of the same type, side by side. Swapping them compiles, and
// the result is a permission check answered about the wrong pair — which, with
// ids that are hard to tell apart by eye, would read as an unrelated bug.
func TestTheUserAndProjectIdsReachTheStoreInThatOrder(t *testing.T) {
	t.Parallel()

	store := &fakeMemberships{}

	if _, err := authz.NewResolver(store).EffectiveRole(t.Context(), "the-user", "the-project"); err != nil {
		t.Fatalf("EffectiveRole(): %v", err)
	}

	if store.gotUser != "the-user" || store.gotProject != "the-project" {
		t.Errorf("store was asked about user %q and project %q", store.gotUser, store.gotProject)
	}

	if store.calls != 1 {
		t.Errorf("the store was consulted %d times, want exactly 1", store.calls)
	}
}
