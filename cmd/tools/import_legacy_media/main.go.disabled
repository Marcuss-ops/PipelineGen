package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"go.uber.org/zap"
	_ "github.com/mattn/go-sqlite3"
	"velox/go-master/internal/repository/media"
	"velox/go-master/internal/storage"
	"velox/go-master/internal/usecase/mediaimport"
)

func main() {
	source := flag.String("source", "", "Source to import: stock, clips, artlist")
	workspaceID := flag.String("workspace", "ws_default", "Workspace ID")
	projectID := flag.String("project", "proj_default", "Project ID")
	oldDBPath := flag.String("old-db", "data/stock.db.sqlite", "Path to old database")
	newDBPath := flag.String("new-db", "data/media.db.sqlite", "Path to new media database")
	dryRun := flag.Bool("dry-run", false, "Dry run mode")
	flag.Parse()

	if *source == "" {
		fmt.Println("Usage: import_legacy_media -source <stock|clips|artlist> [options]")
		os.Exit(1)
	}

	ctx := context.Background()

	logger, _ := zap.NewProduction()
	defer logger.Sync()

	oldSQLiteDB, err := storage.OpenSQLiteDB(*oldDBPath, logger)
	if err != nil {
		log.Fatalf("Failed to open old database: %v", err)
	}
	defer oldSQLiteDB.Close()
	oldDB := oldSQLiteDB.DB

	newSQLiteDB, err := storage.OpenSQLiteDB(*newDBPath, logger)
	if err != nil {
		log.Fatalf("Failed to open new database: %v", err)
	}
	defer newSQLiteDB.Close()
	newDB := newSQLiteDB.DB

	repo := media.NewSQLiteRepository(newDB)

	if *dryRun {
		fmt.Println("DRY RUN MODE - no changes will be made")
	}

	switch *source {
	case "stock":
		importer := mediaimport.NewLegacyStockImporter(oldDB, repo)
		if err := importer.Import(ctx, *workspaceID, *projectID); err != nil {
			log.Fatalf("Failed to import stock: %v", err)
		}
		fmt.Println("Stock import completed")
	case "clips":
		importer := mediaimport.NewLegacyClipsImporter(oldDB, repo)
		if err := importer.Import(ctx, *workspaceID, *projectID); err != nil {
			log.Fatalf("Failed to import clips: %v", err)
		}
		fmt.Println("Clips import completed")
	case "artlist":
		importer := mediaimport.NewLegacyArtlistImporter(oldDB, repo)
		if err := importer.Import(ctx, *workspaceID, *projectID); err != nil {
			log.Fatalf("Failed to import artlist: %v", err)
		}
		fmt.Println("Artlist import completed")
	default:
		fmt.Printf("Unknown source: %s\n", *source)
		os.Exit(1)
	}
}
