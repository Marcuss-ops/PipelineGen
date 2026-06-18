package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

func runBackfillMissing(args []string) error {
	fs := flag.NewFlagSet("backfill-missing", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	limit := fs.Int("limit", 0, "Max number of assets to index (0 for unlimited)")
	source := fs.String("source", "", "Filter by asset source (e.g. stock, youtube, artlist)")
	verbose := fs.Bool("verbose", false, "Print details for each indexed asset")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	deps, coreCleanup, err := app.InitCore(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize core services", zap.Error(err))
	}
	defer coreCleanup()

	if deps.ClipIndexerService == nil {
		return fmt.Errorf("clip indexer service is not initialized or configured")
	}

	ctx := cmdContext()

	fmt.Printf("=== Starting Vector Indexing Backfill ===\n")
	if *source != "" {
		fmt.Printf("Filtering by source: %s\n", *source)
	}
	if *limit > 0 {
		fmt.Printf("Limit set to: %d items\n", *limit)
	}
	fmt.Println()

	// 1. Query all assets that don't have embeddings
	query := `
		SELECT id, source, name 
		FROM media_assets 
		WHERE (embedding_json IS NULL OR embedding_json = '[]' OR embedding_json = '')
	`
	var queryArgs []any
	if *source != "" {
		query += " AND source = ?"
		queryArgs = append(queryArgs, *source)
	}

	// In SQLite, if category/is_folder exists in metadata, we can optionally skip them,
	// but the indexer will handle them gracefully. So we query all media_assets.
	query += " ORDER BY created_at DESC"

	if *limit > 0 {
		query += " LIMIT ?"
		queryArgs = append(queryArgs, *limit)
	}

	rows, err := deps.DB.DB.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return fmt.Errorf("failed to query missing embeddings: %w", err)
	}
	defer rows.Close()

	type assetItem struct {
		ID     string
		Source string
		Name   string
	}

	var items []assetItem
	for rows.Next() {
		var item assetItem
		var nameOpt *string
		if err := rows.Scan(&item.ID, &item.Source, &nameOpt); err != nil {
			return fmt.Errorf("failed to scan row: %w", err)
		}
		if nameOpt != nil {
			item.Name = *nameOpt
		}
		items = append(items, item)
	}

	total := len(items)
	if total == 0 {
		fmt.Println("No assets missing vector embeddings were found. Everything is up-to-date!")
		return nil
	}

	fmt.Printf("Found %d assets requiring vector embedding computation and Qdrant push.\n", total)
	fmt.Println("Starting indexing...")

	successCount := 0
	failedCount := 0

	for i, item := range items {
		select {
		case <-ctx.Done():
			fmt.Println("\nExecution cancelled by user.")
			return ctx.Err()
		default:
		}

		if *verbose {
			fmt.Printf("[%d/%d] Indexing %s (%s) from source '%s'...\n", i+1, total, item.ID, item.Name, item.Source)
		} else if (i+1)%50 == 0 || i+1 == total {
			fmt.Printf("Progress: %d/%d assets processed...\n", i+1, total)
		}

		// Run IndexClip. This computes the text embedding (sentence transformer)
		// and pushes the point to Qdrant.
		start := time.Now()
		err := deps.ClipIndexerService.IndexClip(ctx, item.ID)
		if err != nil {
			failedCount++
			log.Warn("Failed to index asset", zap.String("id", item.ID), zap.Error(err))
			if *verbose {
				fmt.Printf("  ❌ Failed: %v\n", err)
			}
		} else {
			successCount++
			if *verbose {
				fmt.Printf("  ✅ Success (took %v)\n", time.Since(start))
			}
		}
	}

	fmt.Println("\n=== Indexing Backfill Complete ===")
	fmt.Printf("Successfully indexed: %d\n", successCount)
	fmt.Printf("Failed:               %d\n", failedCount)
	fmt.Printf("Total processed:      %d\n", successCount+failedCount)

	return nil
}
