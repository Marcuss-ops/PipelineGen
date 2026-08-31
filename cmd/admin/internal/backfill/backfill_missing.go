package backfill

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

func RunBackfillMissing(args []string) error {
	fs := flag.NewFlagSet("backfill-missing", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	limit := fs.Int("limit", 0, "Max number of assets to index (0 for unlimited)")
	source := fs.String("source", "", "Filter by asset source (e.g. stock, youtube, artlist)")
	verbose := fs.Bool("verbose", false, "Print details for each indexed asset")
	assetIDs := fs.String("asset-ids", "", "Comma-separated asset IDs to force reindex")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, coreCleanup, err := wiring.InitComposition(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize core services", zap.Error(err))
	}
	defer coreCleanup()

	if root.DB == nil || root.DB.DB == nil {
		return fmt.Errorf("database is not initialized or configured")
	}

	mutator, ok := root.CanonicalAssetWriter.(persistence.AssetMutator)
	if !ok || mutator == nil {
		return fmt.Errorf("canonical asset mutator is not available")
	}
	outboxAdapter := outbox.NewRepairAdapter(root.DB.DB, outboxevents.NewRepository(root.DB.DB), outboxevents.ReindexEnvelopeV1Schema, mutator)

	ctx := cli.CmdContext()

	fmt.Printf("=== Starting Vector Indexing Backfill ===\n")
	if *source != "" {
		fmt.Printf("Filtering by source: %s\n", *source)
	}
	if *limit > 0 {
		fmt.Printf("Limit set to: %d items\n", *limit)
	}
	fmt.Println()

	// 1. Query missing embeddings, or an explicit targeted set for metadata repair.
	query := `
		SELECT id, source, name,
			COALESCE(json_extract(metadata_json, '$.content_hash'), json_extract(metadata_json, '$.file_hash'), legacy_file_md5, '') AS content_hash
		FROM media_assets
		WHERE `
	var queryArgs []any
	if strings.TrimSpace(*assetIDs) != "" {
		ids := cli.SplitCSV(*assetIDs)
		placeholders := make([]string, len(ids))
		for i, id := range ids {
			placeholders[i] = "?"
			queryArgs = append(queryArgs, id)
		}
		query += "id IN (" + strings.Join(placeholders, ",") + ")"
	} else {
		query += "(embedding_json IS NULL OR embedding_json = '[]' OR embedding_json = '')"
	}
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

	rows, err := root.DB.DB.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return fmt.Errorf("failed to query missing embeddings: %w", err)
	}
	defer rows.Close()

	type assetItem struct {
		ID          string
		Source      string
		Name        string
		ContentHash string
	}

	var items []assetItem
	for rows.Next() {
		var item assetItem
		var nameOpt, chOpt *string
		if err := rows.Scan(&item.ID, &item.Source, &nameOpt, &chOpt); err != nil {
			return fmt.Errorf("failed to scan row: %w", err)
		}
		if nameOpt != nil {
			item.Name = *nameOpt
		}
		if chOpt != nil && *chOpt != "" {
			item.ContentHash = *chOpt
		} else {
			item.ContentHash = "legacy_no_hash_" + item.ID
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

		// Enqueue an asset.index.requested event with force=true so the
		// worker re-indexes the asset through the canonical outbox path.
		start := time.Now()
		err := outboxAdapter.EnqueueReindex(ctx, item.ID, item.ContentHash, true)
		if err != nil {
			failedCount++
			log.Warn("Failed to enqueue reindex", zap.String("id", item.ID), zap.Error(err))
			if *verbose {
				fmt.Printf("  ❌ Failed: %v\n", err)
			}
		} else {
			successCount++
			if *verbose {
				fmt.Printf("  ✅ Enqueued (took %v)\n", time.Since(start))
			}
		}
	}

	fmt.Println("\n=== Indexing Backfill Complete ===")
	fmt.Printf("Successfully indexed: %d\n", successCount)
	fmt.Printf("Failed:               %d\n", failedCount)
	fmt.Printf("Total processed:      %d\n", successCount+failedCount)

	return nil
}
