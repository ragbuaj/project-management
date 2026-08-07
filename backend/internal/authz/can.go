package authz

import "slices"

// Action names something a caller wants to do. The string is what a log line
// and a test failure show, which is why it reads as a path rather than a
// number nobody can look up.
type Action string

// The actions decided by an account role, from the matrix in
// docs/authorization.md.
//
// Actions belonging to features that do not exist yet are deliberately absent
// — cards, comments, sprints, share links, and the installation-wide powers
// only the owner has. Each arrives with the endpoint that needs it, so that a
// rule and the code it governs are reviewed together. An action with no
// endpoint is a rule nobody can check.
const (
	ActionProjectCreate        Action = "project.create"
	ActionProjectView          Action = "project.view"
	ActionProjectUpdate        Action = "project.update"
	ActionProjectChangeKey     Action = "project.change_key"
	ActionProjectArchive       Action = "project.archive"
	ActionProjectDelete        Action = "project.delete"
	ActionProjectMembersView   Action = "project.members.view"
	ActionProjectMembersManage Action = "project.members.manage"
	ActionProjectMoveToFolder  Action = "project.move_to_folder"

	ActionFolderCreate        Action = "folder.create"
	ActionFolderView          Action = "folder.view"
	ActionFolderRename        Action = "folder.rename"
	ActionFolderDelete        Action = "folder.delete"
	ActionFolderMembersView   Action = "folder.members.view"
	ActionFolderMembersManage Action = "folder.members.manage"
)

// rule is one row of the matrix.
//
// Both fields are written so that the zero value denies. A rule{} added by
// somebody in a hurry grants nothing at all, which is the only default worth
// having in this file — the opposite mistake is invisible until it is a leak.
type rule struct {
	// minimum is the rank the account role must reach. RoleNone grants nothing
	// on its own, so forgetting this field is a refusal rather than an opening.
	minimum Role
	// forbidden is refused for everybody, owner included. Stated rather than
	// left out of the table: a missing entry and a deliberate refusal look the
	// same from the outside, and only one of them should survive somebody
	// adding an endpoint.
	forbidden bool
}

// rules is the matrix in docs/authorization.md, in the form that gets executed.
//
// It answers the **right** half only. Whether the caller may reach the folder
// or project at all is scope.go's question, and both must hold (ADR-0012).
//
// RoleContributor appears nowhere below, and that is expected rather than an
// omission: what separates a contributor from a viewer is writing cards,
// comments, and time logs, and none of those endpoints exist yet. The rank is
// here because users.role stores it today and every card action added later
// will sit at it.
var rules = map[Action]rule{
	// Creating needs no membership — there is nothing to be a member of yet —
	// so these two are the only actions a handler may decide without asking
	// scope.go anything.
	ActionProjectCreate: {minimum: RoleMaintainer},
	ActionFolderCreate:  {minimum: RoleMaintainer},

	ActionProjectView:        {minimum: RoleViewer},
	ActionProjectMembersView: {minimum: RoleViewer},
	ActionFolderView:         {minimum: RoleViewer},
	ActionFolderMembersView:  {minimum: RoleViewer},

	ActionProjectUpdate:        {minimum: RoleMaintainer},
	ActionProjectArchive:       {minimum: RoleMaintainer},
	ActionProjectMembersManage: {minimum: RoleMaintainer},
	ActionProjectMoveToFolder:  {minimum: RoleMaintainer},
	ActionFolderRename:         {minimum: RoleMaintainer},
	ActionFolderDelete:         {minimum: RoleMaintainer},
	ActionFolderMembersManage:  {minimum: RoleMaintainer},

	// The owner's alone. Permanent deletion is the most destructive action in
	// the application, and it is the one place where being able to reach a
	// project is deliberately not enough.
	ActionProjectDelete: {minimum: RoleOwner},

	// Nobody, including the owner. The key is part of every card number ever
	// written into a commit message, a chat, or a document, so changing it
	// rewrites history that lives outside this database.
	ActionProjectChangeKey: {forbidden: true},
}

// Can answers the right half of one row of the matrix.
//
// It says nothing about whether the caller may reach the resource — a
// contributor with no membership anywhere still satisfies Can for
// project.view. Use Resolver.AllowOnProject unless the action needs no
// resource at all, which today means only the two create actions.
//
// An action with no rule is refused. New endpoints therefore arrive denied
// rather than open, and the omission shows up as a request that does not work
// instead of as one that works for everybody.
func Can(caller Caller, action Action) bool {
	r, known := rules[action]
	if !known {
		return false
	}

	return decide(caller, r)
}

// decide reads one rule. It is separate from the lookup so that the properties
// which matter most about a rule — that an empty one grants nothing, that
// forbidden beats a rank that would otherwise allow — can be tested against a
// rule value instead of against a row temporarily pushed into the shared
// table. Doing the latter was a data race: every other test here runs in
// parallel, and they all read that map.
func decide(caller Caller, r rule) bool {
	switch {
	case r.forbidden:
		return false
	case r.minimum == RoleNone:
		return false
	default:
		return caller.Role.AtLeast(r.minimum)
	}
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
