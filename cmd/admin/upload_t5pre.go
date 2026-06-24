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

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

func runUploadT5Pre(_ []string) error {
	log, cleanup, err := productionLogger()
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer cleanup()

	cfg := config.Get()

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
