package main

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"
	"velox/go-master/internal/bootstrap"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/logger"
)

func main() {
	cfg := config.Get()
	if err := cfg.Validate(); err != nil {
		fmt.Printf("Invalid configuration: %v\n", err)
		os.Exit(1)
	}

	logger.Init(cfg.GetLogLevel(), cfg.GetLogFormat())
	zlog := logger.Get()
	defer logger.Sync()

	ctx := context.Background()

	// Initialize core services
	deps, cleanup, err := bootstrap.ExportInitCoreMinimal(cfg, zlog)
	if err != nil {
		zlog.Fatal("Failed to initialize core services", zap.Error(err))
	}
	defer cleanup()

	fmt.Println("Starting full Google Drive synchronization...")

	// 1. Sync Catalog (Stock, Clips, Artlist)
	if deps.CatalogSyncService != nil {
		fmt.Println("Syncing catalog (stock, clips, artlist)...")
		summary, err := deps.CatalogSyncService.SyncAll(ctx)
		if err != nil {
			fmt.Printf("Catalog sync failed: %v\n", err)
		} else {
			fmt.Printf("Catalog sync completed: %d synced, %d failed\n", summary.Synced, summary.Failed)
			for _, root := range summary.Roots {
				fmt.Printf("  - %s: %d synced, %d failed\n", root.Name, root.Synced, root.Failed)
			}
		}
	}

	// 2. Sync Voiceovers
	if deps.VoiceoverSync != nil {
		fmt.Println("Syncing voiceovers...")
		summary, err := deps.VoiceoverSync.Sync(ctx)
		if err != nil {
			fmt.Printf("Voiceover sync failed: %v\n", err)
		} else {
			fmt.Printf("Voiceover sync completed: %d synced, %d failed\n", summary.Synced, summary.Failed)
		}
	}

	// 3. Sync Images
	if deps.ImageService != nil {
		fmt.Println("Syncing images...")
		err := deps.ImageService.SyncFromDrive(ctx)
		if err != nil {
			fmt.Printf("Image sync failed: %v\n", err)
		} else {
			fmt.Println("Image sync completed")
		}
	}

	fmt.Println("Synchronization complete!")
}
