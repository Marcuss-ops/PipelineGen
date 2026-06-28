// cmd/admin/db_migrations.go (June 2026 codex/db-doctor-restore):
//
// `admin db migrations` prints the canonical migration ledger status
// (applied + pending + total + per-row checksums). Replaces the
// older `admin migrate-status` (deleted in this PR) and routes the
// DB handle through DatabaseSet.OpenSet rather than a raw sql.Open,
// so the Check 17 baseline can shrink over time as direct
// `database/sql` calls in cmd/admin/ get migrated.
package main

import (
	"context"
	"flag"
	"fmt"

	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

func runDBMigrations(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("db migrations", flag.ExitOnError)
	dataDir := fs.String("data-dir", "./data", "root data directory")
	// Phase 3 audit #4: strip --app-config before fs.Parse (FlagSet is
	// not aware of it; would otherwise reject it as unknown flag).
	// LoadConfigOrExit operates on the ORIGINAL args so the
	// canonical --app-config <path> is consumed by the loader
	// before any subcommand-specific flags are read.
	fs.Parse(StripAppConfig(args))

	// Phase 3 audit #4: --app-config is mandatory.
	fullCfg := LoadConfigOrExit(args)
	if *dataDir != "" && *dataDir != "./data" {
		fullCfg.Storage.DataDir = *dataDir
	}
	resolved := fullCfg.Storage.ToDatabaseStorageConfig()
	log, _ := zap.NewProduction()
	defer log.Sync()

	ds, err := storage.OpenSet(storage.StorageConfig{
		DataDir:             resolved.DataDir(),
		PrimaryDBPath:       resolved.PrimaryDBPath(),
		ObservabilityDBPath: resolved.ObservabilityDBPath(),
	}, log)
	if err != nil {
		return fmt.Errorf("open set: %w", err)
	}
	defer ds.Close()

	report, err := storage.GetMigrationStatus(ds.Primary.DB, "migrations/sqlite")
	if err != nil {
		return fmt.Errorf("migration status: %w", err)
	}
	fmt.Print(storage.FormatMigrateStatus(report))
	if report.PendingN > 0 {
		return fmt.Errorf("%d pending migrations", report.PendingN)
	}
	return nil
}
