package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	// Registers the pgx driver for the database/sql handle this file opens to
	// create and drop the scratch database.
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/ragbuaj/project-management/backend/internal/postgres"
)

var (
	migrateOnce sync.Once
	migrateErr  error
)

// migrated applies every migration and hands back a pool.
//
// The schema is migrated once per test binary rather than once per test: goose
// keeps its dialect and base filesystem in package-level state, and repeating
// the work only fills the output with "no migrations to run".
func migrated(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := testDatabaseURL(t)

	migrateOnce.Do(func() {
		migrateErr = postgres.Migrate(context.Background(), url)
	})

	if migrateErr != nil {
		t.Fatalf("Migrate(): %v", migrateErr)
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

// Production applies migrations on start-up, so a rolling deploy runs this
// from several processes at once against an empty database. So does `go test
// ./...`, which is how this was found: two test packages migrating in parallel
// both read an empty version table and then raced to CREATE TABLE, and the
// loser failed with a duplicate key on pg_type_typname_nsp_index — an error
// that names a system catalog rather than the actual problem.
//
// A scratch database is created for this, because the shared one is already
// migrated by the time any test runs and racing to do nothing proves nothing.
func TestConcurrentMigrationsDoNotCollide(t *testing.T) {
	url := testDatabaseURL(t)

	admin, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Unique per run so a leftover from a failed run cannot make this pass.
	name := fmt.Sprintf("pm_concurrent_%d", time.Now().UnixNano())

	// CREATE DATABASE cannot run inside a transaction, and the name cannot be
	// a parameter. It is generated above from the clock, never from input.
	if _, err := admin.ExecContext(t.Context(), `CREATE DATABASE `+name); err != nil {
		_ = admin.Close()

		t.Fatalf("create the scratch database: %v", err)
	}

	// Dropping and closing live in one cleanup, in that order. Closing admin
	// with defer instead would run before this and leave the scratch database
	// behind on every run.
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
			t.Errorf("drop the scratch database %s: %v", name, err)
		}

		_ = admin.Close()
	})

	scratchURL, err := replaceDatabase(url, name)
	if err != nil {
		t.Fatalf("build the scratch URL: %v", err)
	}

	const racers = 4

	errs := make(chan error, racers)

	var start sync.WaitGroup

	start.Add(1)

	for range racers {
		go func() {
			// All four wait here so they arrive at the empty version table
			// together, which is the moment the collision happened.
			start.Wait()
			errs <- postgres.Migrate(context.Background(), scratchURL)
		}()
	}

	start.Done()

	for range racers {
		if err := <-errs; err != nil {
			t.Errorf("concurrent Migrate(): %v", err)
		}
	}
}

// replaceDatabase points a connection string at another database on the same
// server, leaving every other parameter alone.
func replaceDatabase(rawURL, name string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	parsed.Path = "/" + name

	return parsed.String(), nil
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
