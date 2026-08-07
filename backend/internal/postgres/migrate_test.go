package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ragbuaj/project-management/backend/internal/postgres"
)

// migrated applies every migration and hands back a pool.
//
// goose keeps its dialect and base filesystem in package-level state, so the
// tests that touch it do not run in parallel with each other.
func migrated(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := testDatabaseURL(t)

	if err := postgres.Migrate(t.Context(), url); err != nil {
		t.Fatalf("Migrate(): %v", err)
	}

	pool, err := postgres.New(t.Context(), url, 5)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	t.Cleanup(pool.Close)

	return pool
}

func TestMigrateRejectsAMalformedURL(t *testing.T) {
	if err := postgres.Migrate(t.Context(), "://not a url"); !errors.Is(err, postgres.ErrInvalidURL) {
		t.Fatalf("err = %v, want ErrInvalidURL", err)
	}
}

// sql.Open does not parse the DSN — it defers that to the first connection,
// where the failure arrives wrapped in an error that quotes the raw string.
// This test is what caught that: the first version of Migrate would have
// printed the database password into the log of any deployment started with a
// malformed DATABASE_URL.
func TestMigrateNeverEchoesTheConnectionString(t *testing.T) {
	const password = "correct-horse-battery-staple"

	err := postgres.Migrate(t.Context(), "://"+password)
	if err == nil {
		t.Fatal("Migrate() = nil, want an error")
	}

	if strings.Contains(err.Error(), password) {
		t.Fatalf("the error leaks the connection string: %v", err)
	}
}

// Applying migrations must be safe to repeat, because production applies them
// on every start-up before the process accepts traffic.
func TestMigrateIsIdempotent(t *testing.T) {
	url := testDatabaseURL(t)

	for i := range 2 {
		if err := postgres.Migrate(t.Context(), url); err != nil {
			t.Fatalf("Migrate() run %d: %v", i+1, err)
		}
	}
}

func TestMigrateCreatesTheIdentityTables(t *testing.T) {
	pool := migrated(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	for _, table := range []string{"users", "sessions", "invitations", "password_resets"} {
		var exists bool

		err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = 'public' AND table_name = $1
			)`, table).Scan(&exists)
		if err != nil {
			t.Fatalf("look up %s: %v", table, err)
		}

		if !exists {
			t.Errorf("table %s was not created", table)
		}
	}
}

// PostgreSQL expands SELECT * when a view is created and then freezes the
// column list, so a _live view defined with * silently stops showing columns
// added later — and a soft-delete leak is exactly the kind of thing nobody
// notices until it matters.
//
// This walks every table that has deleted_at and proves its view still matches.
// It costs nothing to keep and it covers tables that do not exist yet.
func TestLiveViewsMatchTheirTables(t *testing.T) {
	pool := migrated(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	rows, err := pool.Query(ctx, `
		SELECT c.table_name
		FROM information_schema.columns c
		JOIN information_schema.tables t
		  ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = 'public'
		  AND t.table_type = 'BASE TABLE'
		  AND c.column_name = 'deleted_at'
		ORDER BY c.table_name`)
	if err != nil {
		t.Fatalf("list soft-deletable tables: %v", err)
	}

	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect tables: %v", err)
	}

	if len(tables) == 0 {
		t.Fatal("no table with deleted_at found; the query or the schema is wrong")
	}

	for _, table := range tables {
		t.Run(table, func(t *testing.T) {
			tableCols := columnsOf(t, pool, ctx, table)
			viewCols := columnsOf(t, pool, ctx, table+"_live")

			if len(viewCols) == 0 {
				t.Fatalf("view %s_live does not exist", table)
			}

			if len(tableCols) != len(viewCols) {
				t.Fatalf("%s has %d columns but %s_live exposes %d — the view is stale:\ntable: %v\nview:  %v",
					table, len(tableCols), table, len(viewCols), tableCols, viewCols)
			}

			for i := range tableCols {
				if tableCols[i] != viewCols[i] {
					t.Errorf("column %d: table has %q, view has %q", i, tableCols[i], viewCols[i])
				}
			}
		})
	}
}

func columnsOf(t *testing.T, pool *pgxpool.Pool, ctx context.Context, relation string) []string {
	t.Helper()

	rows, err := pool.Query(ctx, `
		SELECT column_name
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1
		ORDER BY column_name`, relation)
	if err != nil {
		t.Fatalf("read columns of %s: %v", relation, err)
	}

	cols, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect columns of %s: %v", relation, err)
	}

	return cols
}

// The glossary defines Owner as one person. Enforcing that in application code
// alone leaves it open to scripts, jobs, and manual queries, so it lives in a
// partial unique index — and this proves the index actually bites.
func TestOnlyOneOwnerIsAllowed(t *testing.T) {
	pool := migrated(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	// Everything below is rolled back, so the shared test database is left
	// exactly as it was found.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM users`); err != nil {
		t.Fatalf("clear users: %v", err)
	}

	const insert = `
		INSERT INTO users (email, name, password_hash, is_owner)
		VALUES ($1, $2, 'argon2id$placeholder', true)`

	if _, err := tx.Exec(ctx, insert, "first@example.test", "First"); err != nil {
		t.Fatalf("insert the first owner: %v", err)
	}

	if _, err := tx.Exec(ctx, insert, "second@example.test", "Second"); err == nil {
		t.Fatal("inserting a second owner succeeded; users_single_owner_key is not enforcing")
	}
}

// A deleted address must not block signing up again with the same address,
// which is why the unique index is partial on deleted_at.
func TestADeletedEmailCanBeReused(t *testing.T) {
	pool := migrated(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	defer func() { _ = tx.Rollback(ctx) }()

	const insert = `
		INSERT INTO users (email, name, password_hash, deleted_at)
		VALUES ($1, 'Reused', 'argon2id$placeholder', $2)`

	if _, err := tx.Exec(ctx, insert, "reuse@example.test", time.Now()); err != nil {
		t.Fatalf("insert the deleted user: %v", err)
	}

	if _, err := tx.Exec(ctx, insert, "reuse@example.test", nil); err != nil {
		t.Fatalf("reusing a deleted address was rejected: %v", err)
	}
}
