package authz

import "slices"

// Action names something a caller wants to do. The string is what a log line
// and a test failure show, which is why it reads as a path rather than a
// number nobody can look up.
type Action string

// The actions decided by a role on a project or on a folder, from the matrix
// in docs/authorization.md.
//
// Actions belonging to features that do not exist yet are deliberately absent
// — cards, comments, sprints, share links, and the installation-wide powers in
// the owner's closed list. Each arrives with the endpoint that needs it, so
// that a rule and the code it governs are reviewed together. An action with no
// endpoint is a rule nobody can check.
const (
	ActionProjectCreate           Action = "project.create"
	ActionProjectView             Action = "project.view"
	ActionProjectUpdate           Action = "project.update"
	ActionProjectChangeKey        Action = "project.change_key"
	ActionProjectArchive          Action = "project.archive"
	ActionProjectDelete           Action = "project.delete"
	ActionProjectMembersView      Action = "project.members.view"
	ActionProjectMembersManage    Action = "project.members.manage"
	ActionProjectRemoveFromFolder Action = "project.remove_from_folder"

	ActionFolderCreate        Action = "folder.create"
	ActionFolderView          Action = "folder.view"
	ActionFolderRename        Action = "folder.rename"
	ActionFolderDelete        Action = "folder.delete"
	ActionFolderMembersView   Action = "folder.members.view"
	ActionFolderMembersManage Action = "folder.members.manage"
)

// Caller is who is asking. It carries no role: a role belongs to a person and
// a resource together, never to the person alone, which is the whole reason
// EffectiveRole takes a project id.
type Caller struct {
	UserID string
	// IsOwner is users.is_owner — the installation owner, one person. It is
	// not a rank above admin: docs/authorization.md gives the owner no rights
	// over the contents of a project they are not a member of.
	IsOwner bool
}

// rule is one row of the matrix.
//
// Every field is written so that its zero value denies. A rule{} added by
// somebody in a hurry grants nothing at all, which is the only default worth
// having in this file — the opposite mistake is invisible until it is a leak.
type rule struct {
	// minimum is the rank needed on the resource the action names. RoleNone
	// grants nothing on its own, so forgetting this field is a refusal rather
	// than an opening.
	minimum Role
	// anySignedIn marks an action with no resource to be a member of yet.
	anySignedIn bool
	// requiresOwner narrows the rule to the installation owner, on top of
	// whatever minimum says. It never widens one.
	requiresOwner bool
	// forbidden is refused for everybody, stated rather than left out of the
	// table — a missing entry and a deliberate refusal look the same from the
	// outside, and only one of them should survive somebody adding an endpoint.
	forbidden bool
}

// rules is the matrix in docs/authorization.md, in the form that gets executed.
//
// Both halves are read against the role for the resource the action names: a
// project action against the effective role from EffectiveRole, a folder
// action against the caller's role in that folder.
var rules = map[Action]rule{
	// Anyone with an account may start something of their own. Self-service
	// registration is a non-goal, so accounts only exist because the owner
	// invited them — that is what keeps this from being open to the world.
	ActionProjectCreate: {anySignedIn: true},
	ActionFolderCreate:  {anySignedIn: true},

	ActionProjectView:        {minimum: RoleViewer},
	ActionProjectMembersView: {minimum: RoleViewer},
	ActionFolderView:         {minimum: RoleViewer},
	ActionFolderMembersView:  {minimum: RoleViewer},

	ActionProjectUpdate:           {minimum: RoleAdmin},
	ActionProjectArchive:          {minimum: RoleAdmin},
	ActionProjectMembersManage:    {minimum: RoleAdmin},
	ActionProjectRemoveFromFolder: {minimum: RoleAdmin},
	ActionFolderRename:            {minimum: RoleAdmin},
	ActionFolderDelete:            {minimum: RoleAdmin},
	ActionFolderMembersManage:     {minimum: RoleAdmin},

	// Permanent deletion is the owner's, and only inside a project they belong
	// to. The owner who needs to delete an abandoned project adds themselves to
	// it first — a step that is recorded and that notifies its members, which
	// is the difference between emergency access and quiet access.
	ActionProjectDelete: {minimum: RoleViewer, requiresOwner: true},

	// Nobody, including the owner. The key is part of every card number ever
	// written into a commit message, a chat, or a document, so changing it
	// rewrites history that lives outside this database.
	ActionProjectChangeKey: {forbidden: true},
}

// Can answers one row of the matrix.
//
// role is the caller's role on the resource this action names — the effective
// role for a project action, the folder role for a folder action. Handing it
// the wrong one is the mistake this signature cannot prevent, which is why
// EffectiveRole exists as the only way to obtain the first.
//
// An action with no rule is refused. New endpoints therefore arrive denied
// rather than open, and the omission shows up as a request that does not work
// instead of as one that works for everybody.
func Can(caller Caller, action Action, role Role) bool {
	r, known := rules[action]

	switch {
	case !known, r.forbidden:
		return false
	case r.anySignedIn:
		return true
	case r.requiresOwner && !caller.IsOwner:
		return false
	case r.minimum == RoleNone:
		return false
	default:
		return role.AtLeast(r.minimum)
	}
}

// CanMoveProjectToFolder is the one decision that needs two roles at once, so
// it is spelled out rather than smuggled through Can with whichever role the
// call site happened to have.
//
// Moving a project into a folder hands it to everyone in that folder
// (ADR-0011). That makes it an access change wearing the clothes of tidying
// up, and it is why both sides have to consent: admin over the project being
// moved, and admin over the folder receiving it.
func CanMoveProjectToFolder(caller Caller, projectRole, folderRole Role) bool {
	return Can(caller, ActionProjectRemoveFromFolder, projectRole) &&
		Can(caller, ActionFolderMembersManage, folderRole)
}

// Actions lists every action this package decides, sorted so the order is
// stable. It exists for tests that must cover the whole table rather than the
// rows somebody remembered.
func Actions() []Action {
	out := make([]Action, 0, len(rules))
	for action := range rules {
		out = append(out, action)
	}

	slices.Sort(out)

	return out
}
