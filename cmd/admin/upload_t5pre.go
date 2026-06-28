// Command upload-t5pre uploads T5-pretrained assets to Google Drive.
//
// One-shot admin utility. Run from the project root:
//
//	./pipelinegen admin upload-t5pre
//
// NOTE (AGENT-1, June 2026): legacy imports replaced with canonical
// versions as part of the wave-internal cmd/admin recovery PR. Pre-fix
// this file imported the removed `internal/{config,storage,upload/drive}`
// packages. Post-fix it imports the canonical
// `internal/infrastructure/{config,database,drive}`.
package main

import (
	"fmt"

	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	// platform/config import removed (audit #4 — fix/admin-config-fail-closed):
	// the fail-closed flow is owned by cmd/admin/utils.go::LoadConfigOrExit so
	// every production-critical subcommand routes through the same canonical
	// chain (--app-config / GetFromPath / Validate / exit 2 on error).
)

func runUploadT5Pre(args []string) error {
	log, cleanup, err := productionLogger()
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer cleanup()

	// Phase 3 audit #4: --app-config is mandatory. LoadConfigOrExit
	// prints "admin: error loading config..." to stderr and exits with
	// code 2 on flag-missing / file-missing / malformed YAML / Validate
	// failure. The silent-fallback-to-defaults behaviour of the legacy
	// config.Get() is banned for production-critical admin ops.
	cfg := LoadConfigOrExit(args)

	ctx := cmdContext()
	driveSvc, err := drive.NewDriveServiceFromFiles(ctx, cfg)
	if err != nil {
		return fmt.Errorf("init drive service: %w", err)
	}

	uploader := &drive.Uploader{Service: driveSvc, Log: log}

	dbPath := cfg.Storage.PrimaryDBFullPath()
	db, err := storage.OpenSQLiteDB(dbPath, log)
	if err != nil {
		return fmt.Errorf("init db (path=%s): %w", dbPath, err)
	}
	defer db.Close()

	if err := db.DB.PingContext(ctx); err != nil {
		return fmt.Errorf("db ping (path=%s): %w", dbPath, err)
	}

	fmt.Println("Clips Root:", cfg.Drive.ClipsRootFolder)
	fmt.Println("Uploader ready:", uploader != nil)
	return nil
}
