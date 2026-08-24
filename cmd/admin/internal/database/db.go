// cmd/admin/db.go (June 2026 codex/db-doctor-restore):
//
// Dispatcher for the admin db {status,check,migrations,backup,restore,rotate}
// subsystem. Each subcommand is implemented in its own file:
//   - cmd/admin/db_status.go     → db status
//   - cmd/admin/db_check.go      → db check  (incl. Qdrant health)
//   - cmd/admin/db_migrations.go → db migrations
//   - cmd/admin/db_backup.go     → db backup
//   - cmd/admin/db_restore.go    → db restore --verify
//   - cmd/admin/db_rotate.go     → db rotate (observability retention, branch 2)
//
// All subcommands route through `internal/platform/sqlite.OpenSet`
// and call helpers in `internal/platform/sqlite/doctor.go` and
// `backup.go`. The admin command itself NEVER calls sql.Open directly —
// the only place in the repo where sql.Open is allowed is
// `internal/platform/sqlite/`.
package database

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func RunDB(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: admin db <subcmd> [args]\n  subcommands: status, check, migrations, backup, restore, rotate")
	}

	// AGENTS.md §7 post-write save ctx — admin `db` composition root;
	// parent Background ctx + signal notify — the admin binary is a
	// one-shot CLI and never has a parent request ctx to inherit.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "status":
		return runDBStatus(ctx, rest)
	case "check":
		return runDBCheck(ctx, rest)
	case "migrations":
		return runDBMigrations(ctx, rest)
	case "backup":
		return runDBBackup(ctx, rest)
	case "restore":
		return runDBRestore(ctx, rest)
	case "rotate":
		return runDBRotate(ctx, rest)
	default:
		return fmt.Errorf("unknown db subcommand: %s (expected: status, check, migrations, backup, restore, rotate)", sub)
	}
}
