package main

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func main() {
	cfg := config.Get()
	log, err := zap.NewProduction()
	if err != nil {
		panic(err)
	}
	defer log.Sync()

	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize composition root", zap.Error(err))
	}
	defer rootCleanup()

	ctx := context.Background()
	folderID := "1qKWzyT-_2PATeacwyGDXWW4O-p1Hg8nB"
	source := "youtube"
	name := "Jackie Chan Clips"
	mediaType := "clip"

	repo := root.Repos.ClipsRepo
	if repo == nil {
		log.Fatal("ClipsRepo is nil")
	}

	fmt.Printf("Syncing folder %s (%s) with source %s...\n", folderID, name, source)
	summary, err := root.Sync.CatalogSync.SyncFolderID(ctx, folderID, source, name, mediaType, repo)
	if err != nil {
		log.Fatal("SyncFolderID failed", zap.Error(err))
	}

	fmt.Printf("Sync completed successfully!\n")
	fmt.Printf("Name: %s\n", summary.Name)
	fmt.Printf("RootFolderID: %s\n", summary.RootFolderID)
	fmt.Printf("Requested: %d\n", summary.Requested)
	fmt.Printf("Synced: %d\n", summary.Synced)
	fmt.Printf("Failed: %d\n", summary.Failed)
}
