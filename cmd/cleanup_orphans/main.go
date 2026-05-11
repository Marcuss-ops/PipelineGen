package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	"velox/go-master/internal/bootstrap"
	"velox/go-master/internal/service/media"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/logger"
)

func main() {
	apply := flag.Bool("apply", false, "Actually delete orphan files (default: dry-run only)")
	dir := flag.String("dir", "", "Assets directory to scan (default: config Storage.DataDir)")
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

	assetsDir := *dir
	if assetsDir == "" {
		assetsDir = cfg.Storage.DataDir
	}
	// Ensure absolute path for consistent comparisons
	if absDir, err := filepath.Abs(assetsDir); err == nil {
		assetsDir = absDir
	}

	// Build a drive uploader from the drive client
	var driveUploader *drive.Uploader
	if deps.DriveClient != nil {
		driveUploader = &drive.Uploader{Service: deps.DriveClient, Log: log}
	}

	// Create deletion service using the same pattern as bootstrap/media.go
	deletionSvc := media.NewDeletionService(
		deps.ArtlistRepo,
		deps.ClipsOnlyRepo,
		deps.StockDriveRepo,
		deps.VoiceoverRepo,
		deps.ImageRepo,
		driveUploader,
		deps.AssetTreeService,
		deps.AssetIndexService,
		log,
	)

	if *apply {
		fmt.Printf("Starting DEEP ORPHAN CLEANUP in %s (APPLY mode - files WILL be deleted)\n", assetsDir)
	} else {
		fmt.Printf("Starting DEEP ORPHAN CLEANUP in %s (DRY RUN - no files will be deleted)\n", assetsDir)
		fmt.Println("Use --apply to actually delete orphan files")
	}
	fmt.Println()

	ctx := context.Background()
	deleted, err := deletionSvc.CleanupOrphanFiles(ctx, assetsDir, !*apply)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Orphan cleanup failed: %v\n", err)
		os.Exit(1)
	}

	if *apply {
		fmt.Printf("\n✅ Cleanup complete: %d orphan files deleted\n", deleted)
	} else {
		fmt.Printf("\n📋 Dry-run complete: %d orphan files would be deleted\n", deleted)
	}
}