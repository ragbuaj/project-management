// Command migrate applies database migrations.
//
// It has no down command on purpose. Migrations are forward-only
// (rules/20-go.md): undoing a schema change means writing a new migration that
// undoes it. A rollback button that exists is a rollback button that gets
// pressed at three in the morning, against production, by someone who has not
// slept.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ragbuaj/project-management/backend/internal/config"
	"github.com/ragbuaj/project-management/backend/internal/postgres"
)

const usage = `usage: migrate <command>

commands:
  up       apply every pending migration
  status   show which migrations are applied and which are pending

New migrations are written by hand as backend/db/migrations/NNNNN_name.sql,
numbered sequentially. Sequential numbers keep the order readable in review;
timestamps do not.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, usage)

		return fmt.Errorf("expected exactly one command, got %d", len(args))
	}

	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch args[0] {
	case "up":
		return postgres.Migrate(ctx, cfg.DatabaseURL)
	case "status":
		return postgres.MigrationStatus(ctx, cfg.DatabaseURL)
	default:
		fmt.Fprint(os.Stderr, usage)

		return fmt.Errorf("unknown command %q", args[0])
	}
}
