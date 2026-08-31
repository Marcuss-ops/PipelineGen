// cmd/admin/db_migrations.go (June 2026 codex/db-doctor-restore):
//
// `admin db migrations` prints the canonical migration ledger status
// (applied + pending + total + per-row checksums). Replaces the
// older `admin migrate-status` (deleted in this PR) and routes the
// DB handle through DatabaseSet.OpenSet rather than a raw sql.Open,
// so the Check 17 baseline can shrink over time as direct
// `database/sql` calls in cmd/admin/ get migrated.
package database

import (
	"context"
	"flag"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"go.uber.org/zap"
)

func RunDBMigrations(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("db migrations", flag.ExitOnError)
	dataDir := fs.String("data-dir", "./data", "root data directory")
	fs.Parse(args)

	fullCfg, err := config.Get()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if *dataDir != "" && *dataDir != "./data" {
		fullCfg.Storage.DataDir = *dataDir
	}
	resolved := fullCfg.Storage.ToDatabaseStorageConfig()
	log, _ := zap.NewProduction()
	defer log.Sync()

	ds, err := storage.OpenSet(storage.StorageConfig{
		DataDir:             resolved.DataDir(),
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
