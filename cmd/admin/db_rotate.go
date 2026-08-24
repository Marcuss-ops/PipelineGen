// cmd/admin/db_rotate.go (June 2026 codex/db-sql-ownership-gate):
//
// `admin db rotate` runs the observability DB retention cycle
// (rotate older rows out of the live DB into a date-stamped backup
// file under <DataDir>/backups/). See ARCHITECTURE.md §12 for the
// retention policy rationale (disposable + cron rotation).
//
// Default knobs:
//
//	-max-age-days  7   (cfg.Storage.ObservabilityMaxAgeDays)
//	-backup-dir    <DataDir>/backups
//
// Output is a JSON line with cutoff / offloaded_to / offloaded_rows
// / purged_rows / bytes_reclaimed / duration_ms.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

func runDBRotate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("db rotate", flag.ExitOnError)
	dataDir := fs.String("data-dir", "./data", "root data directory")
	maxAgeDays := fs.Int("max-age-days", 7, "retention cutoff (days); rows older than this are offloaded + purged")
	backupDir := fs.String("backup-dir", "", "override backup directory (defaults to <DataDir>/backups)")
	fs.Parse(args)

	fullCfg, err := config.Get()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if *dataDir != "" && *dataDir != "./data" {
		fullCfg.Storage.DataDir = *dataDir
	}
	resolved := fullCfg.Storage.ToDatabaseStorageConfig()
	bd := *backupDir
	if bd == "" {
		bd = filepath.Join(*dataDir, "backups")
	}

	if *maxAgeDays <= 0 {
		return fmt.Errorf("-max-age-days must be > 0")
	}

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

	// Use the canonical DatabaseSet.Observability handle. We need to
	// close it manually here, OpenSet already keeps a reference.
	r, err := storage.RotateObservability(ctx, ds.Observability.DB, storage.RotateOptions{
		MaxAgeDays: *maxAgeDays,
		BackupDir:  bd,
		Now:        nil, // wall clock
	})
	if err != nil {
		return fmt.Errorf("rotate: %w", err)
	}

	payload := map[string]any{
		"cutoff":          r.Cutoff.Format(time.RFC3339),
		"offloaded_to":    r.OffloadedTo,
		"offloaded_rows":  r.OffloadedRows,
		"purged_rows":     r.PurgedRows,
		"pre_size_bytes":  r.PreSizeBytes,
		"post_purge_size": r.PostPurgeSize,
		"bytes_reclaimed": r.BytesReclaimed,
		"duration_ms":     r.DurationMs,
	}
	enc := json.NewEncoder(os.Stdout)
	return enc.Encode(payload)
}
