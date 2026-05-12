package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"go.uber.org/zap"

	"velox/go-master/internal/bootstrap"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/logger"
)

func main() {
	apply := flag.Bool("apply", false, "Actually delete folders (default: dry-run only)")
	parentID := flag.String("parent", "1wt4hqmHD5qEsNhpUUBszlRkSHhyFgtGh", "The Stock root folder ID to scan on Drive")
	flag.Parse()

	cfg := config.Get()
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid configuration: %v\n", err)
		os.Exit(1)
	}

	logger.Init(cfg.GetLogLevel(), cfg.GetLogFormat())
	log := logger.Get()
	defer logger.Sync()

	deps, cleanup, err := bootstrap.ExportInitCoreMinimal(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize core services", zap.Error(err))
	}
	defer cleanup()

	var driveUploader *drive.Uploader
	if deps.DriveClient != nil {
		driveUploader = &drive.Uploader{Service: deps.DriveClient, Log: log}
	} else {
		log.Fatal("Drive client is not available")
	}

	ctx := context.Background()

	fmt.Printf("Scanning Drive folder: %s\n", *parentID)
	query := fmt.Sprintf("'%s' in parents and mimeType = 'application/vnd.google-apps.folder' and trashed = false", *parentID)
	
	list, err := deps.DriveClient.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
	if err != nil {
		log.Fatal("Failed to list folders on Drive", zap.Error(err))
	}

	fmt.Printf("Found %d folders on Drive.\n", len(list.Files))

	var orphanFolders []struct{ id, name string }

	for _, f := range list.Files {
		var dummy int
		err := deps.StockDB.DB.QueryRowContext(ctx, "SELECT 1 FROM clips WHERE id = ? AND is_folder = 1", f.Id).Scan(&dummy)
		if err != nil {
			orphanFolders = append(orphanFolders, struct{ id, name string }{f.Id, f.Name})
		}
	}

	if len(orphanFolders) == 0 {
		fmt.Println("No orphan folders found on Drive.")
		return
	}

	fmt.Printf("Found %d orphan folders on Drive (not in DB).\n", len(orphanFolders))
	if !*apply {
		fmt.Println("DRY RUN: The following folders would be DELETED from Drive (use --apply to execute):")
		for _, f := range orphanFolders {
			fmt.Printf("- %s (ID: %s)\n", f.name, f.id)
		}
		return
	}

	fmt.Println("Deleting orphan folders from Drive...")
	for _, f := range orphanFolders {
		fmt.Printf("Deleting %s (%s)... ", f.name, f.id)
		err := driveUploader.DeleteFolder(ctx, f.id)
		if err != nil {
			fmt.Printf("FAILED: %v\n", err)
		} else {
			fmt.Println("OK")
		}
	}

	fmt.Println("\nCleanup complete.")
}
