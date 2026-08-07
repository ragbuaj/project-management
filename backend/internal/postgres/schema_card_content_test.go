package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// seedCard writes one card into the seeded project and hands back its id.
// Everything the card content tables hang off needs one.
func seedCard(t *testing.T, ctx context.Context, tx pgx.Tx, p project, number int, position string) string {
	t.Helper()

	reporter := reporterOf(t, ctx, tx, p)

	var id string
	if err := tx.QueryRow(ctx, insertCard, p.id, number, "Card", p.status, position, reporter).Scan(&id); err != nil {
		t.Fatalf("seed card %d: %v", number, err)
	}

	return id
}

// Deleting a card takes its discussion, its checklists, and its links with it.
// Anything left behind is a row nothing can reach and nothing will ever clean.
func TestDeletingACardTakesItsContentWithIt(t *testing.T) {
	ctx, tx, p := projectTx(t)

	card := seedCard(t, ctx, tx, p, 1, "a0")
	other := seedCard(t, ctx, tx, p, 2, "a1")
	author := reporterOf(t, ctx, tx, p)

	if _, err := tx.Exec(ctx, `
		INSERT INTO comments (card_id, author_id, body) VALUES ($1, $2, 'Looks right')`,
		card, author); err != nil {
		t.Fatalf("comment: %v", err)
	}

	var checklist string

	err := tx.QueryRow(ctx, `
		INSERT INTO checklists (card_id, position) VALUES ($1, 'a0') RETURNING id`, card).Scan(&checklist)
	if err != nil {
		t.Fatalf("checklist: %v", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO checklist_items (checklist_id, content, position)
		VALUES ($1, 'Write the test', 'a0')`, checklist); err != nil {
		t.Fatalf("checklist item: %v", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO card_links (from_card_id, to_card_id, type, created_by)
		VALUES ($1, $2, 'blocks', $3)`, card, other, author); err != nil {
		t.Fatalf("link: %v", err)
	}

	var label string

	err = tx.QueryRow(ctx, `
		INSERT INTO labels (project_id, name, color) VALUES ($1, 'bug', '#ff0000') RETURNING id`,
		p.id).Scan(&label)
	if err != nil {
		t.Fatalf("label: %v", err)
	}

	if _, err := tx.Exec(ctx, `INSERT INTO card_labels (card_id, label_id) VALUES ($1, $2)`,
		card, label); err != nil {
		t.Fatalf("card label: %v", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM cards WHERE id = $1`, card); err != nil {
		t.Fatalf("delete card: %v", err)
	}

	// checklist_items hangs off the card only through its checklist, so this
	// is the cascade that has to travel two hops to be right.
	for _, q := range []struct {
		name string
		sql  string
		arg  string
	}{
		{"comments", `SELECT count(*) FROM comments WHERE card_id = $1`, card},
		{"checklists", `SELECT count(*) FROM checklists WHERE card_id = $1`, card},
		{"checklist_items", `SELECT count(*) FROM checklist_items WHERE checklist_id = $1`, checklist},
		{"card_links", `SELECT count(*) FROM card_links WHERE from_card_id = $1`, card},
		{"card_labels", `SELECT count(*) FROM card_labels WHERE card_id = $1`, card},
	} {
		var left int
		if err := tx.QueryRow(ctx, q.sql, q.arg).Scan(&left); err != nil {
			t.Fatalf("count %s: %v", q.name, err)
		}

		if left != 0 {
			t.Errorf("%d rows left in %s after the card was deleted", left, q.name)
		}
	}
}

// The same relationship must not be storable twice, and a card must not be
// able to block itself. Both are cheap here and expensive in a graph walk.
func TestACardLinkIsNeitherSelfDirectedNorRepeated(t *testing.T) {
	ctx, tx, p := projectTx(t)

	card := seedCard(t, ctx, tx, p, 1, "a0")
	other := seedCard(t, ctx, tx, p, 2, "a1")
	author := reporterOf(t, ctx, tx, p)

	const insert = `
		INSERT INTO card_links (from_card_id, to_card_id, type, created_by)
		VALUES ($1, $2, $3, $4)`

	if err := attempt(t, ctx, tx, insert, card, card, "blocks", author); err == nil {
		t.Error("a card was linked to itself")
	}

	if err := attempt(t, ctx, tx, insert, card, other, "blocks", author); err != nil {
		t.Fatalf("an ordinary link was rejected: %v", err)
	}

	if err := attempt(t, ctx, tx, insert, card, other, "blocks", author); err == nil {
		t.Error("the same link was stored twice")
	}

	// A different type between the same pair is a different relationship.
	if err := attempt(t, ctx, tx, insert, card, other, "relates_to", author); err != nil {
		t.Errorf("a second link of another type was rejected: %v", err)
	}

	// The mirrored row is refused in the service, not here: the constraint
	// cannot see that B→A already exists as A→B. This records that the
	// database lets it through, so nobody mistakes the index for that rule.
	if err := attempt(t, ctx, tx, insert, other, card, "relates_to", author); err != nil {
		t.Errorf("the mirrored link was rejected by the database, which the service relies on handling: %v", err)
	}
}

// A tick with no author is recoverable — an account gets masked and the column
// empties. An author with no tick is not: it means the row was written wrong.
func TestAChecklistItemCannotHaveAnAuthorWithoutBeingDone(t *testing.T) {
	ctx, tx, p := projectTx(t)

	card := seedCard(t, ctx, tx, p, 1, "a0")
	user := reporterOf(t, ctx, tx, p)

	var checklist string

	err := tx.QueryRow(ctx, `
		INSERT INTO checklists (card_id, position) VALUES ($1, 'a0') RETURNING id`, card).Scan(&checklist)
	if err != nil {
		t.Fatalf("checklist: %v", err)
	}

	const insert = `
		INSERT INTO checklist_items (checklist_id, content, position, completed_at, completed_by)
		VALUES ($1, 'Item', $2, $3, $4)`

	if err := attempt(t, ctx, tx, insert, checklist, "a0", nil, user); err == nil {
		t.Error("an item recorded someone as completing it while not being complete")
	}

	if err := attempt(t, ctx, tx, insert, checklist, "a1", time.Now(), nil); err != nil {
		t.Errorf("a completed item with no recorded author was rejected: %v", err)
	}

	if err := attempt(t, ctx, tx, insert, checklist, "a2", time.Now(), user); err != nil {
		t.Errorf("an ordinary completed item was rejected: %v", err)
	}
}

// Ordering uses the same fractional index as cards and statuses (ADR-0003).
// Two items sharing a position inside one checklist would order themselves at
// random between reads.
func TestChecklistOrderIsUniqueWithinItsParent(t *testing.T) {
	ctx, tx, p := projectTx(t)

	card := seedCard(t, ctx, tx, p, 1, "a0")

	var checklist string

	err := tx.QueryRow(ctx, `
		INSERT INTO checklists (card_id, position) VALUES ($1, 'a0') RETURNING id`, card).Scan(&checklist)
	if err != nil {
		t.Fatalf("checklist: %v", err)
	}

	if err := attempt(t, ctx, tx, `
		INSERT INTO checklists (card_id, position) VALUES ($1, 'a0')`, card); err == nil {
		t.Error("two checklists on one card share a position")
	}

	const item = `INSERT INTO checklist_items (checklist_id, content, position) VALUES ($1, 'Item', 'a0')`

	if err := attempt(t, ctx, tx, item, checklist); err != nil {
		t.Fatalf("first item: %v", err)
	}

	if err := attempt(t, ctx, tx, item, checklist); err == nil {
		t.Error("two items in one checklist share a position")
	}
}

// Removing a label from the project removes it from every card. Removing it
// from one card leaves the label itself alone — the two directions of the join
// table are not symmetric and getting them the wrong way round deletes data
// nobody asked to delete.
func TestRemovingALabelIsNotRemovingTheCard(t *testing.T) {
	ctx, tx, p := projectTx(t)

	card := seedCard(t, ctx, tx, p, 1, "a0")

	var label string

	err := tx.QueryRow(ctx, `
		INSERT INTO labels (project_id, name, color) VALUES ($1, 'bug', '#ff0000') RETURNING id`,
		p.id).Scan(&label)
	if err != nil {
		t.Fatalf("label: %v", err)
	}

	if _, err := tx.Exec(ctx, `INSERT INTO card_labels (card_id, label_id) VALUES ($1, $2)`,
		card, label); err != nil {
		t.Fatalf("attach: %v", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM card_labels WHERE card_id = $1 AND label_id = $2`,
		card, label); err != nil {
		t.Fatalf("detach: %v", err)
	}

	var cards, labels int

	if err := tx.QueryRow(ctx, `SELECT count(*) FROM cards WHERE id = $1`, card).Scan(&cards); err != nil {
		t.Fatalf("count cards: %v", err)
	}

	if err := tx.QueryRow(ctx, `SELECT count(*) FROM labels WHERE id = $1`, label).Scan(&labels); err != nil {
		t.Fatalf("count labels: %v", err)
	}

	if cards != 1 || labels != 1 {
		t.Errorf("detaching a label left %d cards and %d labels, want 1 and 1", cards, labels)
	}

	// The other direction: deleting the label detaches it everywhere.
	if _, err := tx.Exec(ctx, `INSERT INTO card_labels (card_id, label_id) VALUES ($1, $2)`,
		card, label); err != nil {
		t.Fatalf("re-attach: %v", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM labels WHERE id = $1`, label); err != nil {
		t.Fatalf("delete label: %v", err)
	}

	var attached int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM card_labels WHERE card_id = $1`, card).Scan(&attached); err != nil {
		t.Fatalf("count attachments: %v", err)
	}

	if attached != 0 {
		t.Errorf("%d attachments survived their label", attached)
	}
}

// A comment is soft-deleted so the thread keeps its shape. The body still has
// to be something, because an empty comment is a UI element with nothing in it.
func TestACommentNeedsABody(t *testing.T) {
	ctx, tx, p := projectTx(t)

	card := seedCard(t, ctx, tx, p, 1, "a0")
	author := reporterOf(t, ctx, tx, p)

	const insert = `INSERT INTO comments (card_id, author_id, body) VALUES ($1, $2, $3)`

	if err := attempt(t, ctx, tx, insert, card, author, ""); err == nil {
		t.Error("an empty comment was accepted")
	}

	if err := attempt(t, ctx, tx, insert, card, author, "Fine"); err != nil {
		t.Errorf("an ordinary comment was rejected: %v", err)
	}
}
