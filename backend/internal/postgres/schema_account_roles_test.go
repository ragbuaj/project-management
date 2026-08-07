package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func accountTx(t *testing.T) (context.Context, pgx.Tx) {
	t.Helper()

	pool := migrated(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	return ctx, tx
}

const insertUserWithRole = `
	INSERT INTO users (email, name, password_hash, role)
	VALUES ($1, 'Seed', 'argon2id$placeholder', $2)`

// The vocabulary of ADR-0012 and nothing else. A value the application never
// writes would be read by authz as a role it has never heard of, and "deny by
// default" is not obviously what happens when the role itself is unknown.
func TestAnAccountRoleOutsideTheFourIsRejected(t *testing.T) {
	ctx, tx := accountTx(t)

	for _, role := range []string{"admin", "member", "maintainer_", "Owner", "", "superuser"} {
		if err := attempt(t, ctx, tx, insertUserWithRole, "bad-"+role+"@example.test", role); err == nil {
			t.Errorf("account role %q was accepted", role)
		}
	}

	for _, role := range []string{"maintainer", "contributor", "viewer"} {
		if err := attempt(t, ctx, tx, insertUserWithRole, role+"@example.test", role); err != nil {
			t.Errorf("account role %q was rejected: %v", role, err)
		}
	}
}

// The glossary defines Owner as one person. The rule has not changed, only the
// column it reads — so it has to still bite from the new side.
func TestOnlyOneAccountMayHoldTheOwnerRole(t *testing.T) {
	ctx, tx := accountTx(t)

	if _, err := tx.Exec(ctx, `DELETE FROM users`); err != nil {
		t.Fatalf("clear users: %v", err)
	}

	if err := attempt(t, ctx, tx, insertUserWithRole, "first@example.test", "owner"); err != nil {
		t.Fatalf("insert the first owner: %v", err)
	}

	if err := attempt(t, ctx, tx, insertUserWithRole, "second@example.test", "owner"); err == nil {
		t.Error("a second owner was accepted; users_single_owner_role_key is not enforcing")
	}
}

// Membership is binary now (ADR-0012). An insert that names only the pair has
// to be enough — if a NOT NULL role column had survived, this would fail.
func TestMembershipNeedsNothingButThePair(t *testing.T) {
	ctx, tx := accountTx(t)

	var userID string

	err := tx.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash)
		VALUES ('member-seed@example.test', 'Seed', 'argon2id$placeholder')
		RETURNING id`).Scan(&userID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	var folderID string
	if err := tx.QueryRow(ctx, `INSERT INTO folders (name, created_by) VALUES ('F', $1) RETURNING id`, userID).Scan(&folderID); err != nil {
		t.Fatalf("seed folder: %v", err)
	}

	var projectID string

	err = tx.QueryRow(ctx, `
		INSERT INTO projects (key, name, created_by) VALUES ('MEMB', 'M', $1) RETURNING id`,
		userID).Scan(&projectID)
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}

	if _, err := tx.Exec(ctx, `INSERT INTO project_members (project_id, user_id) VALUES ($1, $2)`, projectID, userID); err != nil {
		t.Errorf("project membership without a role was rejected: %v", err)
	}

	if _, err := tx.Exec(ctx, `INSERT INTO folder_members (folder_id, user_id) VALUES ($1, $2)`, folderID, userID); err != nil {
		t.Errorf("folder membership without a role was rejected: %v", err)
	}
}

// invitations.role now carries the ACCOUNT role the invitee will be created
// with. 'owner' is absent on purpose: there is exactly one, and that account is
// the first one rather than an invited one.
func TestAnInvitationCarriesAnAccountRoleAndNeverOwner(t *testing.T) {
	ctx, tx := accountTx(t)

	var invitedBy string

	err := tx.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash)
		VALUES ('inviter@example.test', 'Inviter', 'argon2id$placeholder')
		RETURNING id`).Scan(&invitedBy)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	const invite = `
		INSERT INTO invitations (email, token_hash, invited_by, role, expires_at)
		VALUES ($1, sha256(convert_to($1, 'UTF8')), $2, $3, now() + interval '7 days')`

	for _, role := range []string{"owner", "admin", "member", "Contributor", ""} {
		if err := attempt(t, ctx, tx, invite, role+"@example.test", invitedBy, role); err == nil {
			t.Errorf("invitation role %q was accepted", role)
		}
	}

	for _, role := range []string{"maintainer", "contributor", "viewer"} {
		if err := attempt(t, ctx, tx, invite, role+"-inv@example.test", invitedBy, role); err != nil {
			t.Errorf("invitation role %q was rejected: %v", role, err)
		}
	}
}
