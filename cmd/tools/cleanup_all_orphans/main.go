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

	if deps.DriveClient == nil {
		log.Fatal("Drive client is not available")
	}
	driveUploader := &drive.Uploader{Service: deps.DriveClient, Log: log}

	targets := []struct {
		name     string
		rootID   string
		dbPrefix string
	}{
		{"Artlist", "1OAAf5dawAppdopsgCq1yHFGPUXCI9Vbk", "artlist"},
		{"Stock", "1wt4hqmHD5qEsNhpUUBszlRkSHhyFgtGh", "stock"},
		{"YouTube Clips", "1r4B_m3Gz_5f2f5O-vNqG6_G8_G8_G8_G", "clips"},
	}

	ctx := context.Background()

	for _, t := range targets {
		if t.rootID == "" || t.rootID == "1r4B_m3Gz_5f2f5O-vNqG6_G8_G8_G8_G" {
			fmt.Printf("\n--- Skipping %s: Root ID not configured or placeholder ---\n", t.name)
			continue
		}

		fmt.Printf("\n--- Checking %s (Root: %s) ---\n", t.name, t.rootID)
		query := fmt.Sprintf("'%s' in parents and mimeType = 'application/vnd.google-apps.folder' and trashed = false", t.rootID)
		list, err := deps.DriveClient.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
		if err != nil {
			fmt.Printf("Error listing %s: %v\n", t.name, err)
			continue
		}

		fmt.Printf("Found %d folders on Drive.\n", len(list.Files))

		var orphans []struct{ id, name string }
		for _, f := range list.Files {
			var dummy int
			var dbErr error
			switch t.dbPrefix {
			case "artlist":
				dbErr = deps.ArtlistDB.DB.QueryRowContext(ctx, "SELECT 1 FROM clips WHERE id = ?", f.Id).Scan(&dummy)
			case "stock":
				dbErr = deps.StockDB.DB.QueryRowContext(ctx, "SELECT 1 FROM clips WHERE id = ?", f.Id).Scan(&dummy)
			case "clips":
				dbErr = deps.YouTubeDB.DB.QueryRowContext(ctx, "SELECT 1 FROM clips WHERE id = ?", f.Id).Scan(&dummy)
			}

			if dbErr != nil {
				orphans = append(orphans, struct{ id, name string }{f.Id, f.Name})
			}
		}

		if len(orphans) == 0 {
			fmt.Printf("No orphan folders found for %s.\n", t.name)
			continue
		}

		fmt.Printf("Found %d orphan folders for %s.\n", len(orphans), t.name)
		if !*apply {
			for _, f := range orphans {
				fmt.Printf("  - [DRY RUN] Would delete: %s (%s)\n", f.name, f.id)
			}
		} else {
			for _, f := range orphans {
				fmt.Printf("  - Deleting %s (%s)... ", f.name, f.id)
				err := driveUploader.DeleteFolder(ctx, f.id)
				if err != nil {
					fmt.Printf("FAILED: %v\n", err)
				} else {
					fmt.Println("OK")
				}
			}
		}
	}
}
