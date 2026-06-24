// Command upload-t5pre uploads T5-pretrained assets to Google Drive.
//
// One-shot admin utility. Run from the project root:
//
//	./pipelinegen admin upload-t5pre
package main

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/config"
	"github.com/Marcuss-ops/PipelineGen/internal/storage"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
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

	dbPath := cfg.Storage.DataDir + "/media/media.db.sqlite"
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
