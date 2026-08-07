package authz

import "testing"

// The most important property of the rules table cannot be seen from outside
// the package: a row added in a hurry, with no field set, must grant nothing.
//
// The opposite default is the one that never announces itself. A rule that
// accidentally allows everybody produces no error, no failing request, and no
// log line — just somebody reading what was never theirs.
func TestARuleWithNothingFilledInGrantsNothing(t *testing.T) {
	t.Parallel()

	const madeUp Action = "test.rule_with_no_fields"

	rules[madeUp] = rule{}
	t.Cleanup(func() { delete(rules, madeUp) })

	for _, role := range []Role{RoleNone, RoleViewer, RoleMember, RoleAdmin} {
		for _, owner := range []bool{false, true} {
			if Can(Caller{UserID: "u", IsOwner: owner}, madeUp, role) {
				t.Errorf("an empty rule allowed owner=%v role=%v", owner, role)
			}
		}
	}
}

// forbidden has to refuse on its own, not because the only row using it also
// happens to have no minimum.
//
// project.change_key is that row today, so removing the flag from Can changes
// nothing and every test still passes — which would leave a field that reads
// as a guarantee and enforces nothing. The day somebody writes a rule that
// grants and forbids at once, the flag has to be the half that wins.
func TestForbiddenRefusesEvenWhenARankWouldOtherwiseAllow(t *testing.T) {
	t.Parallel()

	const madeUp Action = "test.forbidden_with_a_minimum"

	rules[madeUp] = rule{minimum: RoleViewer, forbidden: true}
	t.Cleanup(func() { delete(rules, madeUp) })

	for _, role := range []Role{RoleViewer, RoleMember, RoleAdmin} {
		if Can(Caller{UserID: "u", IsOwner: true}, madeUp, role) {
			t.Errorf("a forbidden action was allowed for %v", role)
		}
	}
}
