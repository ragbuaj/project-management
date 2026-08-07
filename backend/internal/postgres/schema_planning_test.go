package postgres_test

import (
	"testing"
	"time"
)

// Two timers running at once both keep counting, and the second one is always
// an accident — a tab left open, a stop that did not reach the server. The
// partial unique index is what makes starting the second one fail loudly.
func TestOnlyOneTimerRunsPerPerson(t *testing.T) {
	ctx, tx, p := projectTx(t)

	card := seedCard(t, ctx, tx, p, 1, "a0")
	other := seedCard(t, ctx, tx, p, 2, "a1")
	user := reporterOf(t, ctx, tx, p)

	const start = `INSERT INTO time_logs (card_id, user_id, started_at) VALUES ($1, $2, $3)`

	if err := attempt(t, ctx, tx, start, card, user, time.Now()); err != nil {
		t.Fatalf("first timer: %v", err)
	}

	if err := attempt(t, ctx, tx, start, other, user, time.Now()); err == nil {
		t.Fatal("a second timer started while the first was still running")
	}

	// Stopping the first frees the slot. A finished log needs its duration,
	// which is what time_logs_duration insists on.
	//
	// The end is derived from started_at rather than from now(): inside a
	// transaction now() is the instant the transaction began, which is before
	// the started_at written a moment ago, and time_logs_period rightly
	// refuses that.
	if _, err := tx.Exec(ctx, `
		UPDATE time_logs
		SET ended_at = started_at + interval '1 minute', duration_seconds = 60
		WHERE user_id = $1`, user); err != nil {
		t.Fatalf("stop the first timer: %v", err)
	}

	if err := attempt(t, ctx, tx, start, other, user, time.Now()); err != nil {
		t.Errorf("stopping the first timer did not free the slot: %v", err)
	}
}

// A stopped timer with no duration makes every report quietly short, and a
// duration on a running one is a number nobody computed. Both halves move
// together or the row is refused.
func TestAFinishedTimeLogCarriesItsDuration(t *testing.T) {
	ctx, tx, p := projectTx(t)

	card := seedCard(t, ctx, tx, p, 1, "a0")
	user := reporterOf(t, ctx, tx, p)

	started := time.Now()

	const insert = `
		INSERT INTO time_logs (card_id, user_id, started_at, ended_at, duration_seconds)
		VALUES ($1, $2, $3, $4, $5)`

	if err := attempt(t, ctx, tx, insert, card, user, started, started.Add(time.Minute), nil); err == nil {
		t.Error("a stopped timer with no duration was accepted")
	}

	if err := attempt(t, ctx, tx, insert, card, user, started, nil, 60); err == nil {
		t.Error("a running timer with a duration was accepted")
	}

	// Ending before starting is not a short session, it is a bad clock.
	if err := attempt(t, ctx, tx, insert, card, user, started, started.Add(-time.Minute), 60); err == nil {
		t.Error("a timer that ended before it started was accepted")
	}

	if err := attempt(t, ctx, tx, insert, card, user, started, started.Add(time.Minute), 60); err != nil {
		t.Errorf("an ordinary finished log was rejected: %v", err)
	}
}

// NULL means "from any status", and NULL is never equal to NULL — so a plain
// UNIQUE would let that rule be declared as many times as anyone likes. The
// index folds NULL onto a fixed uuid to close it.
func TestTheFromAnyStatusTransitionCanOnlyBeDeclaredOnce(t *testing.T) {
	ctx, tx, p := projectTx(t)

	var second string

	err := tx.QueryRow(ctx, `
		INSERT INTO statuses (project_id, name, category, position)
		VALUES ($1, 'Done', 'done', 'a1') RETURNING id`, p.id).Scan(&second)
	if err != nil {
		t.Fatalf("second status: %v", err)
	}

	const insert = `
		INSERT INTO workflow_transitions (project_id, from_status_id, to_status_id)
		VALUES ($1, $2, $3)`

	if err := attempt(t, ctx, tx, insert, p.id, nil, second); err != nil {
		t.Fatalf("the from-anywhere transition was rejected: %v", err)
	}

	if err := attempt(t, ctx, tx, insert, p.id, nil, second); err == nil {
		t.Error("the from-anywhere transition was declared twice")
	}

	// A transition from a named status to the same target is a different rule.
	if err := attempt(t, ctx, tx, insert, p.id, p.status, second); err != nil {
		t.Errorf("a transition from a named status was rejected: %v", err)
	}

	if err := attempt(t, ctx, tx, insert, p.id, second, second); err == nil {
		t.Error("a status was declared as transitioning to itself")
	}
}

// A rule whose action fires another rule's trigger is a loop, and a loop in an
// automation engine writes rows until someone notices. The cap belongs in the
// database, not in whoever is on call.
func TestAutomationRunsCannotNestPastTheCap(t *testing.T) {
	ctx, tx, p := projectTx(t)

	author := reporterOf(t, ctx, tx, p)

	var rule string

	err := tx.QueryRow(ctx, `
		INSERT INTO automation_rules (project_id, name, trigger, actions, created_by)
		VALUES ($1, 'Assign on move', '{}'::jsonb, '[]'::jsonb, $2)
		RETURNING id`, p.id, author).Scan(&rule)
	if err != nil {
		t.Fatalf("rule: %v", err)
	}

	const run = `
		INSERT INTO automation_runs (rule_id, event_id, depth, state)
		VALUES ($1, gen_random_uuid(), $2, 'succeeded')`

	if err := attempt(t, ctx, tx, run, rule, 5); err != nil {
		t.Errorf("a run at the cap was rejected: %v", err)
	}

	if err := attempt(t, ctx, tx, run, rule, 6); err == nil {
		t.Error("a run past the cap was accepted")
	}

	if err := attempt(t, ctx, tx, run, rule, -1); err == nil {
		t.Error("a run at negative depth was accepted")
	}
}

// One channel per kind per person. Two Telegram chats on one account would
// both receive everything, and "send it to them" would stop having one answer.
func TestOneNotificationChannelPerKind(t *testing.T) {
	ctx, tx, p := projectTx(t)

	user := reporterOf(t, ctx, tx, p)

	const insert = `INSERT INTO notification_channels (user_id, kind, address) VALUES ($1, $2, $3)`

	if err := attempt(t, ctx, tx, insert, user, "telegram", "12345"); err != nil {
		t.Fatalf("first channel: %v", err)
	}

	if err := attempt(t, ctx, tx, insert, user, "telegram", "67890"); err == nil {
		t.Error("a second telegram channel was accepted")
	}

	if err := attempt(t, ctx, tx, insert, user, "email", "someone@example.test"); err != nil {
		t.Errorf("a channel of another kind was rejected: %v", err)
	}

	if err := attempt(t, ctx, tx, insert, user, "carrier_pigeon", "coo"); err == nil {
		t.Error("an unknown channel kind was accepted")
	}
}

// Saved filter names are compared without case, because "My work" and "my
// work" in one sidebar is a list nobody can use. A filter with no project runs
// across all of them, which is a different filter, not a missing one.
func TestSavedFilterNamesAreUniquePerOwnerWithoutCase(t *testing.T) {
	ctx, tx, p := projectTx(t)

	owner := reporterOf(t, ctx, tx, p)

	const insert = `
		INSERT INTO saved_filters (owner_id, project_id, name, query)
		VALUES ($1, $2, $3, '{}'::jsonb)`

	if err := attempt(t, ctx, tx, insert, owner, p.id, "My work"); err != nil {
		t.Fatalf("first filter: %v", err)
	}

	if err := attempt(t, ctx, tx, insert, owner, nil, "my work"); err == nil {
		t.Error("the same name in another case was accepted")
	}

	if err := attempt(t, ctx, tx, insert, owner, nil, "Everything"); err != nil {
		t.Errorf("a cross-project filter was rejected: %v", err)
	}
}

// Deleting a project has to take its automation, its filters, and its
// notifications with it. A rule left behind still fires; a notification left
// behind links to a project that is gone.
func TestDeletingAProjectRemovesWhatHangsOffIt(t *testing.T) {
	ctx, tx, p := projectTx(t)

	owner := reporterOf(t, ctx, tx, p)

	if _, err := tx.Exec(ctx, `
		INSERT INTO automation_rules (project_id, name, trigger, actions, created_by)
		VALUES ($1, 'Rule', '{}'::jsonb, '[]'::jsonb, $2)`, p.id, owner); err != nil {
		t.Fatalf("rule: %v", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO saved_filters (owner_id, project_id, name, query)
		VALUES ($1, $2, 'Filter', '{}'::jsonb)`, owner, p.id); err != nil {
		t.Fatalf("filter: %v", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO notifications (user_id, event_id, project_id, kind)
		VALUES ($1, gen_random_uuid(), $2, 'mention')`, owner, p.id); err != nil {
		t.Fatalf("notification: %v", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO recurring_cards (project_id, template, rrule, timezone, next_run_at)
		VALUES ($1, '{}'::jsonb, 'FREQ=WEEKLY;BYDAY=MO', 'Asia/Jakarta', now())`, p.id); err != nil {
		t.Fatalf("recurring card: %v", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM projects WHERE id = $1`, p.id); err != nil {
		t.Fatalf("delete project: %v", err)
	}

	for _, table := range []string{"automation_rules", "saved_filters", "notifications", "recurring_cards"} {
		var left int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE project_id = $1`, p.id).Scan(&left); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}

		if left != 0 {
			t.Errorf("%d rows left in %s after the project was deleted", left, table)
		}
	}
}
