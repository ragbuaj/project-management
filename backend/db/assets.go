// Package db carries the SQL assets that are embedded into the binaries, so a
// deployed image never depends on files being present next to it.
//
// It sits here rather than under internal/ because go:embed can only reach
// files at or below the directory of the file declaring it, and the migration
// directory is documented as backend/db/migrations in architecture.md, in
// lefthook.yml, and in the migrations CI workflow.
package db

import "embed"

// Migrations holds every goose migration, in filename order.
//
//go:embed migrations/*.sql
var Migrations embed.FS
