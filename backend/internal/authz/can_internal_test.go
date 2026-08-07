package authz

import "testing"

// These two properties cannot be seen from outside the package, and the first
// attempt at testing them pushed a temporary row into the shared rules map.
// Every other test in this binary runs in parallel and reads that map, so it
// was a data race — invisible locally, where the detector needs cgo, and
// caught by CI on an unrelated documentation PR because it only fails
// sometimes. decide takes a rule value instead, so there is nothing shared to
// race on.
func TestARuleWithNothingFilledInGrantsNothing(t *testing.T) {
	t.Parallel()

	// The opposite default is the one that never announces itself. A rule that
	// accidentally allows everybody produces no error, no failing request, and
	// no log line — just somebody reading what was never theirs.
	for _, role := range []Role{RoleNone, RoleViewer, RoleContributor, RoleMaintainer, RoleOwner} {
		if decide(Caller{UserID: "u", Role: role}, rule{}) {
			t.Errorf("an empty rule allowed %v", role)
		}
	}
}

// forbidden has to refuse on its own, not because the only row using it today
// also happens to have no minimum.
//
// project.change_key is that row, so dropping the flag from the decision
// changes nothing and every other test still passes — which would leave a
// field that reads as a guarantee and enforces nothing. The day somebody
// writes a rule that grants and forbids at once, the flag has to be the half
// that wins.
func TestForbiddenRefusesEvenWhenARankWouldOtherwiseAllow(t *testing.T) {
	t.Parallel()

	forbidding := rule{minimum: RoleViewer, forbidden: true}

	for _, role := range []Role{RoleViewer, RoleContributor, RoleMaintainer, RoleOwner} {
		if decide(Caller{UserID: "u", Role: role}, forbidding) {
			t.Errorf("a forbidden action was allowed for %v", role)
		}
	}
}
