package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// folderTx seeds a user and a folder inside a transaction the caller rolls
// back, so the shared test database is left exactly as it was found.
func folderTx(t *testing.T) (context.Context, pgx.Tx, string, string) {
	t.Helper()

	pool := migrated(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	var userID string

	err = tx.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash)
		VALUES ('folder-seed@example.test', 'Seed', 'argon2id$placeholder')
		RETURNING id`).Scan(&userID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	var folderID string

	err = tx.QueryRow(ctx, `
		INSERT INTO folders (name, created_by) VALUES ('Klien', $1) RETURNING id`,
		userID).Scan(&folderID)
	if err != nil {
		t.Fatalf("seed folder: %v", err)
	}

	return ctx, tx, userID, folderID
}

// The promise in ADR-0011 that costs the most if it is wrong: deleting a
// folder releases its projects, it does not take them with it. One action that
// throws away other people's work must not be named "delete folder".
//
// Enforced by the foreign key rather than by the service, so that "the folder
// was deleted" and "the projects survived" are not two separate things to
// remember.
func TestDeletingAFolderReleasesItsProjectsInsteadOfDeletingThem(t *testing.T) {
	ctx, tx, userID, folderID := folderTx(t)

	var projectID string

	err := tx.QueryRow(ctx, `
		INSERT INTO projects (key, name, created_by, folder_id)
		VALUES ('FLD', 'Inside a folder', $1, $2)
		RETURNING id`, userID, folderID).Scan(&projectID)
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM folders WHERE id = $1`, folderID); err != nil {
		t.Fatalf("delete the folder: %v", err)
	}

	var (
		survived bool
		folder   *string
	)

	err = tx.QueryRow(ctx, `
		SELECT true, folder_id FROM projects WHERE id = $1`, projectID).Scan(&survived, &folder)
	if err != nil {
		t.Fatalf("the project did not survive its folder: %v", err)
	}

	if folder != nil {
		t.Errorf("folder_id = %q, want NULL — the project still points at a folder that is gone", *folder)
	}
}

// Membership follows the folder into the grave. A row naming a folder that no
// longer exists would be read by authz as a right over nothing, and it would
// come back to life if the id were ever reused.
func TestDeletingAFolderRemovesItsMemberships(t *testing.T) {
	ctx, tx, userID, folderID := folderTx(t)

	if _, err := tx.Exec(ctx, `
		INSERT INTO folder_members (folder_id, user_id)
		VALUES ($1, $2)`, folderID, userID); err != nil {
		t.Fatalf("seed membership: %v", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM folders WHERE id = $1`, folderID); err != nil {
		t.Fatalf("delete the folder: %v", err)
	}

	var left int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM folder_members WHERE folder_id = $1`, folderID).Scan(&left); err != nil {
		t.Fatalf("count memberships: %v", err)
	}

	if left != 0 {
		t.Errorf("%d membership rows outlived their folder", left)
	}
}

// A project without a folder is the normal case, not an edge one (ADR-0011).
// Requiring a folder would mean inventing a "General" one that nobody meant to
// create and that nobody meant to be a member of.
func TestAProjectMayStandOnItsOwn(t *testing.T) {
	ctx, tx, userID, _ := folderTx(t)

	if _, err := tx.Exec(ctx, `
		INSERT INTO projects (key, name, created_by)
		VALUES ('SOLO', 'No folder', $1)`, userID); err != nil {
		t.Errorf("a project without a folder was rejected: %v", err)
	}
}

// A project may not point at a folder that was never there. Without this, a
// mistyped id would produce a project nobody in the folder can see and nobody
// outside it can find.
func TestAProjectCannotNameAFolderThatDoesNotExist(t *testing.T) {
	ctx, tx, userID, _ := folderTx(t)

	err := attempt(t, ctx, tx, `
		INSERT INTO projects (key, name, created_by, folder_id)
		VALUES ('GHOST', 'Ghost folder', $1, '01998aaa-0000-7000-8000-000000000000')`, userID)
	if err == nil {
		t.Error("a project naming a folder that does not exist was accepted")
	}
}

// Whoever created a folder is still referenced by it, so removing that account
// has to be refused rather than silently cascade. Same rule projects already
// carry: RESTRICT, because the alternative is deleting a folder — and with it
// the membership of everyone in it — as a side effect of an account cleanup.
func TestAFolderKeepsItsCreatorFromBeingDeleted(t *testing.T) {
	ctx, tx, userID, _ := folderTx(t)

	if err := attempt(t, ctx, tx, `DELETE FROM users WHERE id = $1`, userID); err == nil {
		t.Error("deleting the creator of a folder was allowed")
	}
}
