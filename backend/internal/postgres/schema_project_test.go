package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// project seeds a user, a project, a status, and a board inside tx. Everything
// is rolled back by the caller, so the shared test database is left untouched.
type project struct {
	id     string
	status string
	board  string
}

func seedProject(t *testing.T, ctx context.Context, tx pgx.Tx) project {
	t.Helper()

	var p project

	var userID string

	err := tx.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash)
		VALUES ('seed@example.test', 'Seed', 'argon2id$placeholder')
		RETURNING id`).Scan(&userID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	err = tx.QueryRow(ctx, `
		INSERT INTO projects (key, name, created_by)
		VALUES ('SEED', 'Seed project', $1)
		RETURNING id`, userID).Scan(&p.id)
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}

	err = tx.QueryRow(ctx, `
		INSERT INTO statuses (project_id, name, category, position)
		VALUES ($1, 'Todo', 'todo', 'a0')
		RETURNING id`, p.id).Scan(&p.status)
	if err != nil {
		t.Fatalf("seed status: %v", err)
	}

	err = tx.QueryRow(ctx, `
		INSERT INTO boards (project_id, name, position)
		VALUES ($1, 'Main', 'a0')
		RETURNING id`, p.id).Scan(&p.board)
	if err != nil {
		t.Fatalf("seed board: %v", err)
	}

	return p
}

// attempt runs one statement inside a savepoint and hands back its error.
//
// PostgreSQL aborts the whole transaction when any statement fails, so without
// a savepoint every statement after an expected failure comes back as 25P02
// rather than as its own result. A test that mixes rejections and acceptances
// needs each attempt isolated.
func attempt(t *testing.T, ctx context.Context, tx pgx.Tx, sql string, args ...any) error {
	t.Helper()

	savepoint, err := tx.Begin(ctx)
	if err != nil {
		t.Fatalf("savepoint: %v", err)
	}

	if _, execErr := savepoint.Exec(ctx, sql, args...); execErr != nil {
		_ = savepoint.Rollback(ctx)

		return execErr
	}

	if err := savepoint.Commit(ctx); err != nil {
		t.Fatalf("release savepoint: %v", err)
	}

	return nil
}

func projectTx(t *testing.T) (context.Context, pgx.Tx, project) {
	t.Helper()

	pool := migrated(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	return ctx, tx, seedProject(t, ctx, tx)
}

// The project key becomes part of every card number ever written into a commit
// message, a chat, or a document, which is why authorization.md forbids
// changing it afterwards. Its shape is therefore worth refusing up front.
func TestProjectKeyMustLookLikeAKey(t *testing.T) {
	ctx, tx, p := projectTx(t)

	var createdBy string
	if err := tx.QueryRow(ctx, `SELECT created_by FROM projects WHERE id = $1`, p.id).Scan(&createdBy); err != nil {
		t.Fatalf("read created_by: %v", err)
	}

	const insert = `INSERT INTO projects (key, name, created_by) VALUES ($1, 'x', $2)`

	for _, key := range []string{"pm", "P M", "1PM", "", "TOOLONGAKEY12"} {
		if err := attempt(t, ctx, tx, insert, key, createdBy); err == nil {
			t.Errorf("key %q was accepted", key)
		}
	}

	for _, key := range []string{"PM", "PM2", "ABCDEFGHIJ"} {
		if err := attempt(t, ctx, tx, insert, key, createdBy); err != nil {
			t.Errorf("key %q was rejected: %v", key, err)
		}
	}
}

// ADR-0004: a board column is presentation. Removing a board must leave the
// statuses — and therefore the cards holding them — completely alone.
func TestDeletingABoardLeavesItsStatusesAlone(t *testing.T) {
	ctx, tx, p := projectTx(t)

	if _, err := tx.Exec(ctx, `
		INSERT INTO board_columns (board_id, status_id, name, position)
		VALUES ($1, $2, 'Todo', 'a0')`, p.board, p.status); err != nil {
		t.Fatalf("seed column: %v", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM boards WHERE id = $1`, p.board); err != nil {
		t.Fatalf("delete board: %v", err)
	}

	var columns int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM board_columns WHERE board_id = $1`, p.board).Scan(&columns); err != nil {
		t.Fatalf("count columns: %v", err)
	}

	if columns != 0 {
		t.Errorf("board deletion left %d columns behind", columns)
	}

	var statuses int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM statuses WHERE id = $1`, p.status).Scan(&statuses); err != nil {
		t.Fatalf("count statuses: %v", err)
	}

	if statuses != 1 {
		t.Error("deleting a board removed the status; cards would lose their state")
	}
}

// The other direction: a status a column still shows is not something to
// remove by accident, so the foreign key is RESTRICT rather than CASCADE.
func TestAStatusStillShownByAColumnCannotBeDeleted(t *testing.T) {
	ctx, tx, p := projectTx(t)

	if _, err := tx.Exec(ctx, `
		INSERT INTO board_columns (board_id, status_id, name, position)
		VALUES ($1, $2, 'Todo', 'a0')`, p.board, p.status); err != nil {
		t.Fatalf("seed column: %v", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM statuses WHERE id = $1`, p.status); err == nil {
		t.Fatal("deleting a status still shown by a column succeeded")
	}
}

// Two columns showing the same status would make one card appear twice on the
// same board.
func TestOneColumnPerStatusPerBoard(t *testing.T) {
	ctx, tx, p := projectTx(t)

	const insert = `
		INSERT INTO board_columns (board_id, status_id, name, position)
		VALUES ($1, $2, $3, 'a0')`

	if _, err := tx.Exec(ctx, insert, p.board, p.status, "Todo"); err != nil {
		t.Fatalf("first column: %v", err)
	}

	if _, err := tx.Exec(ctx, insert, p.board, p.status, "Todo again"); err == nil {
		t.Fatal("a second column for the same status was accepted")
	}
}

// The constraint 00001 could not declare. Without it an invitation could point
// at a project that no longer exists, and accepting it would grant membership
// of nothing.
func TestInvitationsPointAtRealProjects(t *testing.T) {
	ctx, tx, p := projectTx(t)

	var invitedBy string
	if err := tx.QueryRow(ctx, `SELECT created_by FROM projects WHERE id = $1`, p.id).Scan(&invitedBy); err != nil {
		t.Fatalf("read created_by: %v", err)
	}

	const insert = `
		INSERT INTO invitations (email, token_hash, invited_by, project_id, role, expires_at)
		VALUES ($1, $2, $3, $4, 'member', now() + interval '7 days')`

	token := make([]byte, 32)

	if _, err := tx.Exec(ctx, insert, "a@example.test", token, invitedBy, p.id); err != nil {
		t.Fatalf("invitation to a real project was rejected: %v", err)
	}

	token[0] = 1

	_, err := tx.Exec(ctx, insert, "b@example.test", token, invitedBy,
		"00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Fatal("invitation to a non-existent project was accepted")
	}
}
