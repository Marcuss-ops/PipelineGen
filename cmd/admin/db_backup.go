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

	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

func runDBBackup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("db backup", flag.ExitOnError)
	dataDir := fs.String("data-dir", "./data", "root data directory")
	src := fs.String("src", "", "source DB path (default: resolved primary)")
	out := fs.String("out", "", "output backup path (required)")
	// Phase 3 audit #4: strip --app-config before fs.Parse (FlagSet is
	// not aware of it; would otherwise reject it as unknown flag).
	// LoadConfigOrExit operates on the ORIGINAL args so the
	// canonical --app-config <path> is consumed by the loader
	// before any subcommand-specific flags are read.
	fs.Parse(StripAppConfig(args))

	if *out == "" {
		return fmt.Errorf("-out is required")
	}

	// Phase 3 audit #4: --app-config is mandatory. LoadConfigOrExit
	// prints "admin: error loading config..." to stderr and exits
	// with code 2 on flag-missing / file-missing / malformed YAML /
	// Validate failure. The silent-fallback-to-defaults behaviour
	// of the legacy config.Get() is banned for production-critical
	// admin ops (audit #4 — fix/admin-config-fail-closed).
	fullCfg := LoadConfigOrExit(args)
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

	payload := map[string]interface{}{
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
