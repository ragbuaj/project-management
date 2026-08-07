package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	// Registers the pgx driver under the name "pgx" for database/sql, which is
	// the interface goose speaks.
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/ragbuaj/project-management/backend/db"
)

const migrationsDir = "migrations"

// Migrate applies every pending migration and returns once the schema is at
// the newest version.
//
// There is no Down. rules/20-go.md requires forward-only migrations: undoing a
// schema change means writing a new migration that undoes it. cmd/migrate
// deliberately exposes no command that would roll back, because a button that
// exists is a button that gets pressed at three in the morning.
func Migrate(ctx context.Context, databaseURL string) error {
	sqlDB, err := openForMigrations(databaseURL)
	if err != nil {
		return err
	}

	defer func() { _ = sqlDB.Close() }()

	if err := goose.UpContext(ctx, sqlDB, migrationsDir); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	return nil
}

// MigrationStatus writes the applied and pending migrations to goose's logger.
func MigrationStatus(ctx context.Context, databaseURL string) error {
	sqlDB, err := openForMigrations(databaseURL)
	if err != nil {
		return err
	}

	defer func() { _ = sqlDB.Close() }()

	if err := goose.StatusContext(ctx, sqlDB, migrationsDir); err != nil {
		return fmt.Errorf("read status: %w", err)
	}

	return nil
}

// openForMigrations validates the connection string, then opens a database/sql
// handle for goose.
//
// It opens its own connection rather than borrowing the pgxpool. Migrations run
// once at start-up and then never again, so a short-lived connection with its
// own lifetime is easier to reason about than one sharing the pool that will
// serve traffic.
func openForMigrations(databaseURL string) (*sql.DB, error) {
	// sql.Open does not parse the DSN. It defers that to the first connection,
	// where the failure surfaces wrapped in an error that embeds the raw
	// string — password and all. Parsing here keeps the connection string out
	// of every log line that reports a bad configuration.
	if _, err := pgxpool.ParseConfig(databaseURL); err != nil {
		return nil, ErrInvalidURL
	}

	sqlDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, ErrInvalidURL
	}

	// goose keeps the dialect and the base filesystem in package-level state,
	// so this is set on every call rather than once in an init function.
	if err := goose.SetDialect("postgres"); err != nil {
		_ = sqlDB.Close()

		return nil, fmt.Errorf("set dialect: %w", err)
	}

	goose.SetBaseFS(db.Migrations)

	return sqlDB, nil
}
