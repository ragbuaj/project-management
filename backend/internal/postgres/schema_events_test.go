package postgres_test

import (
	"testing"
	"time"
)

// insertEvent writes one activity event and hands back the partition it landed
// in, which is the only way to see the routing from outside.
const insertEvent = `
	INSERT INTO activity_events (project_id, entity_type, entity_id, action, occurred_at)
	VALUES ($1, 'card', gen_random_uuid(), $2, $3)
	RETURNING tableoid::regclass::text`

// Partitioning has to be there from the first row. Adding it to a table
// already holding tens of millions of events means rewriting the table, and
// this is the only one in the system that grows without a bound.
func TestActivityEventsIsPartitionedByMonth(t *testing.T) {
	ctx, tx, _ := projectTx(t)

	var kind string

	err := tx.QueryRow(ctx, `
		SELECT relkind FROM pg_class WHERE oid = 'activity_events'::regclass`).Scan(&kind)
	if err != nil {
		t.Fatalf("read relkind: %v", err)
	}

	if kind != "p" {
		t.Fatalf("activity_events relkind is %q, want \"p\" (partitioned)", kind)
	}

	var months int

	err = tx.QueryRow(ctx, `
		SELECT count(*) FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid
		WHERE i.inhparent = 'activity_events'::regclass
		  AND c.relname ~ '^activity_events_[0-9]{4}_[0-9]{2}$'`).Scan(&months)
	if err != nil {
		t.Fatalf("count partitions: %v", err)
	}

	// This month plus three ahead: the runway the migration lays down before
	// the monthly job of Phase 1 exists to extend it.
	if months < 4 {
		t.Errorf("found %d monthly partitions, want at least 4", months)
	}
}

// The partition named 2026_08 must hold exactly the UTC month of that name.
// The boundary is computed in the migration, and computing it from the
// session's TimeZone instead would cut months at a different instant on a
// server configured differently — leaving the names and the ranges out of step
// with nothing to notice it.
//
// Both edges are checked from outside: the first microsecond of the UTC month
// belongs to this month's partition, and the microsecond before it does not.
func TestMonthPartitionsAreCutAtUTCMidnight(t *testing.T) {
	ctx, tx, p := projectTx(t)

	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	var first string
	if err := tx.QueryRow(ctx, insertEvent, p.id, "card.created", monthStart).Scan(&first); err != nil {
		t.Fatalf("insert at the month start: %v", err)
	}

	if want := "activity_events_" + monthStart.Format("2006_01"); first != want {
		t.Errorf("the first instant of the month landed in %s, want %s", first, want)
	}

	// The migration lays down this month and the three after it, so the
	// instant before this month has no partition of its own and falls through
	// to DEFAULT. That is what proves the cut is where it should be.
	var before string
	if err := tx.QueryRow(ctx, insertEvent, p.id, "card.created", monthStart.Add(-time.Microsecond)).Scan(&before); err != nil {
		t.Fatalf("insert just before the month start: %v", err)
	}

	if before != "activity_events_default" {
		t.Errorf("the instant before the month landed in %s, want activity_events_default", before)
	}
}

func TestAnEventLandsInItsOwnMonthPartition(t *testing.T) {
	ctx, tx, p := projectTx(t)

	now := time.Now().UTC()

	var partition string
	if err := tx.QueryRow(ctx, insertEvent, p.id, "card.created", now).Scan(&partition); err != nil {
		t.Fatalf("insert: %v", err)
	}

	want := "activity_events_" + now.Format("2006_01")
	if partition != want {
		t.Errorf("event landed in %s, want %s", partition, want)
	}
}

// The event is written in the same transaction as the change that caused it
// (ADR-0002), so an event PostgreSQL refuses to route fails the user's
// request. A history table must never be able to take the application down,
// which is what the DEFAULT partition is for.
func TestAnEventBeyondEveryMonthPartitionStillLands(t *testing.T) {
	ctx, tx, p := projectTx(t)

	beyond := time.Now().UTC().AddDate(5, 0, 0)

	var partition string
	if err := tx.QueryRow(ctx, insertEvent, p.id, "card.created", beyond).Scan(&partition); err != nil {
		t.Fatalf("an event outside every monthly partition was rejected: %v", err)
	}

	if partition != "activity_events_default" {
		t.Errorf("event landed in %s, want activity_events_default", partition)
	}
}

// The cascade has to reach through the partitions, not just the parent.
func TestDeletingAProjectRemovesItsEvents(t *testing.T) {
	ctx, tx, p := projectTx(t)

	now := time.Now().UTC()

	for _, at := range []time.Time{now, now.AddDate(5, 0, 0)} {
		if _, err := tx.Exec(ctx, insertEvent, p.id, "card.created", at); err != nil {
			t.Fatalf("insert at %s: %v", at, err)
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM projects WHERE id = $1`, p.id); err != nil {
		t.Fatalf("delete project: %v", err)
	}

	var left int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM activity_events WHERE project_id = $1`, p.id).Scan(&left); err != nil {
		t.Fatalf("count events: %v", err)
	}

	if left != 0 {
		t.Errorf("%d events outlived their project, one of them in DEFAULT", left)
	}
}

// Delivery order is the outbox id order. GENERATED ALWAYS rather than BY
// DEFAULT means no caller can insert an id of its own and put itself at the
// front of the queue.
func TestOutboxIdsCannotBeChosenByTheCaller(t *testing.T) {
	ctx, tx, _ := projectTx(t)

	err := attempt(t, ctx, tx, `
		INSERT INTO outbox (id, event_id, topic, payload)
		VALUES (1, gen_random_uuid(), 'card.moved', '{}'::jsonb)`)
	if err == nil {
		t.Error("a caller chose its own outbox id")
	}

	err = attempt(t, ctx, tx, `
		INSERT INTO outbox (event_id, topic, payload)
		VALUES (gen_random_uuid(), 'card.moved', '{}'::jsonb)`)
	if err != nil {
		t.Errorf("an ordinary outbox write was rejected: %v", err)
	}
}

// Queue depth is the first health metric in this system (ADR-0002), and the
// worker reads only what it has not sent. A full index would grow with the
// table; this one grows with the backlog, which is the number that matters.
func TestTheOutboxPendingIndexCoversOnlyTheBacklog(t *testing.T) {
	ctx, tx, _ := projectTx(t)

	var partial bool

	err := tx.QueryRow(ctx, `
		SELECT indpred IS NOT NULL FROM pg_index
		WHERE indexrelid = 'outbox_pending_idx'::regclass`).Scan(&partial)
	if err != nil {
		t.Fatalf("read outbox_pending_idx: %v", err)
	}

	if !partial {
		t.Error("outbox_pending_idx is not partial; it will grow with the table, not the backlog")
	}
}

// The list grows every phase, which is why action is free text — but
// entity_type is closed, because a typo there silently hides an entity's whole
// history from the activity feed that filters on it.
func TestAnUnknownEntityTypeIsRejected(t *testing.T) {
	ctx, tx, p := projectTx(t)

	err := attempt(t, ctx, tx, `
		INSERT INTO activity_events (project_id, entity_type, entity_id, action)
		VALUES ($1, 'crad', gen_random_uuid(), 'crad.created')`, p.id)
	if err == nil {
		t.Error("an event about a 'crad' was accepted")
	}

	err = attempt(t, ctx, tx, `
		INSERT INTO activity_events (project_id, entity_type, entity_id, action)
		VALUES ($1, 'card', gen_random_uuid(), '  ')`, p.id)
	if err == nil {
		t.Error("an event with a blank action was accepted")
	}
}
