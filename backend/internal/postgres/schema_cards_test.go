package postgres_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// insertCard writes one card with the columns that have no default and hands
// back its id. Everything else is left to the table.
const insertCard = `
	INSERT INTO cards (project_id, number, title, status_id, position, reporter_id)
	VALUES ($1, $2, $3, $4, $5, $6)
	RETURNING id`

// reporterOf returns a user id usable as reporter_id: the person who created
// the seeded project.
func reporterOf(t *testing.T, ctx context.Context, tx pgx.Tx, p project) string {
	t.Helper()

	var id string
	if err := tx.QueryRow(ctx, `SELECT created_by FROM projects WHERE id = $1`, p.id).Scan(&id); err != nil {
		t.Fatalf("read created_by: %v", err)
	}

	return id
}

// The most expensive bug this schema can have: a card holding a status that
// belongs to another project disappears from every board, and nothing anywhere
// raises an error. The composite foreign key is the only thing standing
// between that and a support ticket nobody can reproduce.
func TestACardCannotCarryAnotherProjectsStatus(t *testing.T) {
	ctx, tx, p := projectTx(t)

	reporter := reporterOf(t, ctx, tx, p)

	var otherProject, otherStatus string

	err := tx.QueryRow(ctx, `
		INSERT INTO projects (key, name, created_by)
		VALUES ('OTHER', 'Other project', $1)
		RETURNING id`, reporter).Scan(&otherProject)
	if err != nil {
		t.Fatalf("seed the other project: %v", err)
	}

	err = tx.QueryRow(ctx, `
		INSERT INTO statuses (project_id, name, category, position)
		VALUES ($1, 'Todo', 'todo', 'a0')
		RETURNING id`, otherProject).Scan(&otherStatus)
	if err != nil {
		t.Fatalf("seed the other status: %v", err)
	}

	if err := attempt(t, ctx, tx, insertCard, p.id, 1, "Borrowed status", otherStatus, "a0", reporter); err == nil {
		t.Fatal("a card took a status belonging to another project")
	}

	if err := attempt(t, ctx, tx, insertCard, p.id, 2, "Own status", p.status, "a0", reporter); err != nil {
		t.Fatalf("a card with its own project's status was rejected: %v", err)
	}
}

// The composite key is the only foreign key on status_id, so it also has to
// carry what a single-column one would have: a status some card still holds
// cannot be removed. Losing that would leave cards pointing at nothing.
func TestAStatusStillHeldByACardCannotBeDeleted(t *testing.T) {
	ctx, tx, p := projectTx(t)

	reporter := reporterOf(t, ctx, tx, p)

	if _, err := tx.Exec(ctx, insertCard, p.id, 1, "In progress", p.status, "a0", reporter); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := attempt(t, ctx, tx, `DELETE FROM statuses WHERE id = $1`, p.status); err == nil {
		t.Fatal("a status still held by a card was deleted")
	}
}

// A card cannot be deleted while a subtask still points at it. NO ACTION
// refuses this exactly as RESTRICT would; the difference between them only
// shows when one statement removes both, which the project cascade covers.
func TestACardWithSubtasksCannotBeDeleted(t *testing.T) {
	ctx, tx, p := projectTx(t)

	reporter := reporterOf(t, ctx, tx, p)

	var parent string
	if err := tx.QueryRow(ctx, insertCard, p.id, 1, "Parent", p.status, "a0", reporter).Scan(&parent); err != nil {
		t.Fatalf("parent: %v", err)
	}

	var child string
	if err := tx.QueryRow(ctx, insertCard, p.id, 2, "Child", p.status, "a1", reporter).Scan(&child); err != nil {
		t.Fatalf("child: %v", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE cards SET parent_card_id = $1 WHERE id = $2`, parent, child); err != nil {
		t.Fatalf("link: %v", err)
	}

	if err := attempt(t, ctx, tx, `DELETE FROM cards WHERE id = $1`, parent); err == nil {
		t.Fatal("a card with a subtask was deleted, orphaning it")
	}
}

// ADR-0003 scopes ordering to (project, status) and only among the cards a
// board shows. Archiving must free the position back up, otherwise a column
// that has churned for a year starts refusing new cards.
func TestCardOrderIsUniquePerStatusWhileVisible(t *testing.T) {
	ctx, tx, p := projectTx(t)

	reporter := reporterOf(t, ctx, tx, p)

	var first string
	if err := tx.QueryRow(ctx, insertCard, p.id, 1, "First", p.status, "a0", reporter).Scan(&first); err != nil {
		t.Fatalf("first card: %v", err)
	}

	if err := attempt(t, ctx, tx, insertCard, p.id, 2, "Same slot", p.status, "a0", reporter); err == nil {
		t.Fatal("two visible cards share one position in the same status")
	}

	if _, err := tx.Exec(ctx, `UPDATE cards SET archived_at = now() WHERE id = $1`, first); err != nil {
		t.Fatalf("archive the first card: %v", err)
	}

	if err := attempt(t, ctx, tx, insertCard, p.id, 3, "Reused slot", p.status, "a0", reporter); err != nil {
		t.Fatalf("archiving did not free the position: %v", err)
	}
}

// PostgreSQL 18 makes VIRTUAL the default for generated columns (ADR-0007),
// and a virtual column cannot carry a GIN index. Losing the STORED keyword
// would not fail the migration — it would leave search working and unindexed
// until the table is large enough for anyone to notice.
func TestSearchVectorIsStoredAndMatches(t *testing.T) {
	ctx, tx, p := projectTx(t)

	var generated string

	err := tx.QueryRow(ctx, `
		SELECT attgenerated FROM pg_attribute
		WHERE attrelid = 'cards'::regclass AND attname = 'search_tsv'`).Scan(&generated)
	if err != nil {
		t.Fatalf("read attgenerated: %v", err)
	}

	if generated != "s" {
		t.Errorf("search_tsv is %q, want \"s\" (stored); a virtual column cannot be indexed with GIN", generated)
	}

	reporter := reporterOf(t, ctx, tx, p)

	if _, err := tx.Exec(ctx, insertCard, p.id, 1, "Reconcile the ledger", p.status, "a0", reporter); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var found int

	err = tx.QueryRow(ctx, `
		SELECT count(*) FROM cards
		WHERE project_id = $1 AND search_tsv @@ to_tsquery('simple', 'ledger')`, p.id).Scan(&found)
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if found != 1 {
		t.Errorf("search for a word in the title found %d cards, want 1", found)
	}
}

// "The current sprint" appears in the burndown chart, the sprint board, and
// every report that scopes itself to it. Two active sprints would make that
// phrase mean whichever row came back first.
func TestOnlyOneActiveSprintPerProject(t *testing.T) {
	ctx, tx, p := projectTx(t)

	const insert = `
		INSERT INTO sprints (project_id, name, state, start_at, end_at)
		VALUES ($1, $2, 'active', now(), now() + interval '14 days')`

	if _, err := tx.Exec(ctx, insert, p.id, "Sprint 1"); err != nil {
		t.Fatalf("first active sprint: %v", err)
	}

	if err := attempt(t, ctx, tx, insert, p.id, "Sprint 2"); err == nil {
		t.Fatal("a second active sprint was accepted")
	}

	if err := attempt(t, ctx, tx, `
		INSERT INTO sprints (project_id, name) VALUES ($1, 'Sprint 3')`, p.id); err != nil {
		t.Fatalf("a second planned sprint was rejected: %v", err)
	}
}

// An active sprint with no dates cannot be burned down, so the state and the
// period are constrained together rather than separately.
func TestAnActiveSprintNeedsAPeriod(t *testing.T) {
	ctx, tx, p := projectTx(t)

	err := attempt(t, ctx, tx, `
		INSERT INTO sprints (project_id, name, state) VALUES ($1, 'Undated', 'active')`, p.id)
	if err == nil {
		t.Error("an active sprint without dates was accepted")
	}

	err = attempt(t, ctx, tx, `
		INSERT INTO sprints (project_id, name, state, start_at, end_at)
		VALUES ($1, 'Backwards', 'active', now(), now() - interval '1 day')`, p.id)
	if err == nil {
		t.Error("a sprint ending before it starts was accepted")
	}
}

// The retention job hard-deletes projects thirty days after they go to the
// trash, and that single statement removes the project's statuses and its
// cards together. This is why cards_status_same_project and parent_card_id
// carry NO ACTION rather than RESTRICT: RESTRICT is checked row by row and
// would refuse, unable to see that the referencing rows are going too.
func TestDeletingAProjectRemovesItsCardsAndSprints(t *testing.T) {
	ctx, tx, p := projectTx(t)

	reporter := reporterOf(t, ctx, tx, p)

	var parent string
	if err := tx.QueryRow(ctx, insertCard, p.id, 1, "Parent", p.status, "a0", reporter).Scan(&parent); err != nil {
		t.Fatalf("parent card: %v", err)
	}

	var child string
	if err := tx.QueryRow(ctx, insertCard, p.id, 2, "Child", p.status, "a1", reporter).Scan(&child); err != nil {
		t.Fatalf("child card: %v", err)
	}

	var sprint string

	err := tx.QueryRow(ctx, `
		INSERT INTO sprints (project_id, name) VALUES ($1, 'Sprint 1') RETURNING id`, p.id).Scan(&sprint)
	if err != nil {
		t.Fatalf("sprint: %v", err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE cards SET parent_card_id = $1, epic_id = $1, sprint_id = $2 WHERE id = $3`,
		parent, sprint, child)
	if err != nil {
		t.Fatalf("link the child to its parent: %v", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM projects WHERE id = $1`, p.id); err != nil {
		t.Fatalf("hard-deleting a project failed; the retention job cannot run: %v", err)
	}

	var left int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM cards WHERE project_id = $1`, p.id).Scan(&left); err != nil {
		t.Fatalf("count cards: %v", err)
	}

	if left != 0 {
		t.Errorf("%d cards outlived their project", left)
	}
}

// A card cannot be its own parent or its own epic. Both are one-line CHECKs
// and both close a loop that the recursive walk in the service would otherwise
// have to survive.
func TestACardIsNotItsOwnParent(t *testing.T) {
	ctx, tx, p := projectTx(t)

	reporter := reporterOf(t, ctx, tx, p)

	var id string
	if err := tx.QueryRow(ctx, insertCard, p.id, 1, "Only card", p.status, "a0", reporter).Scan(&id); err != nil {
		t.Fatalf("insert: %v", err)
	}

	for _, column := range []string{"parent_card_id", "epic_id"} {
		if err := attempt(t, ctx, tx, `UPDATE cards SET `+column+` = id WHERE id = $1`, id); err == nil {
			t.Errorf("a card became its own %s", column)
		}
	}
}
