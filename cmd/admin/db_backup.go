// cmd/admin/db_backup.go (June 2026 codex/db-doctor-restore):
//
// `admin db backup` runs the canonical Backup helper against the
// specified source DB and writes a JSON-line result containing
// path + size + sha256 + duration. The output is consumable by CI
// scripts that grep the sha256 into a manifest.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

func runDBBackup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("db backup", flag.ExitOnError)
	dataDir := fs.String("data-dir", "./data", "root data directory")
	src := fs.String("src", "", "source DB path (default: resolved primary)")
	out := fs.String("out", "", "output backup path (required)")
	fs.Parse(args)

	if *out == "" {
		return fmt.Errorf("-out is required")
	}

	fullCfg, err := config.Get()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if *dataDir != "" && *dataDir != "./data" {
		fullCfg.Storage.DataDir = *dataDir
	}
	resolved := fullCfg.Storage.ToDatabaseStorageConfig()
	srcPath := *src
	if srcPath == "" {
		srcPath = resolved.PrimaryDBPath()
	}

	log, _ := zap.NewProduction()
	defer log.Sync()

	r, err := storage.Backup(srcPath, *out)
	if err != nil {
		return err
	}

	payload := map[string]any{
		"path":         r.Path,
		"src":          srcPath,
		"size_bytes":   r.SizeBytes,
		"sha256":       r.SHA256,
		"duration_ms":  r.DurationMs,
		"completed_at": r.CompletedAt.Format(time.RFC3339),
	}
	enc := json.NewEncoder(os.Stdout)
	return enc.Encode(payload)
}
