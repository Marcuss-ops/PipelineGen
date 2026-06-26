// cmd/admin/reindex_qdrant.go — QDRANT-003 (June 2026)
//
// One-shot reindex of media_assets into Qdrant using the canonical
// IndexWriter.ReindexAll pipeline (AssetStore → PayloadMapper → IndexWriter).
// This command replaces the legacy reindex (reindex.go, removed in QDRANT-003)
// which used raw SQL + VectorAsset directly without schema validation.
//
// Usage:
//
//	go run ./cmd/admin reindex-qdrant                           # dry-run (counts only)
//	go run ./cmd/admin reindex-qdrant --apply                    # reindex into canonical collection
//	go run ./cmd/admin reindex-qdrant --apply --target-collection=media_assets_v4  # explicit target
//	go run ./cmd/admin reindex-qdrant --apply --limit=500        # cap rows
//	go run ./cmd/admin reindex-qdrant --json                     # machine-readable dry-run
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
)

// reindexQdrantDeps holds the parsed flags for runReindexQdrant.
type reindexQdrantDeps struct {
	Apply            bool
	JSON             bool
	DryRun           bool
	Limit            int
	TargetCollection string
}

// parseReindexQdrantArgs parses CLI args into reindexQdrantDeps.
// Flags:
//
//	--apply              actually write to Qdrant (default: dry-run)
//	--dry-run            explicit dry-run (default, omit when --apply)
//	--json               machine-readable output
//	--limit=N            cap number of assets
//	--target-collection=X  override target collection name
func parseReindexQdrantArgs(args []string) (reindexQdrantDeps, error) {
	deps := reindexQdrantDeps{}
	for _, a := range args {
		a = strings.TrimSpace(a)
		switch {
		case a == "--apply":
			deps.Apply = true
		case a == "--dry-run":
			deps.DryRun = true
		case a == "--json":
			deps.JSON = true
		case strings.HasPrefix(a, "--limit="):
			n, err := parsePositiveFlag(a, "--limit")
			if err != nil {
				return deps, err
			}
			deps.Limit = n
		case strings.HasPrefix(a, "--target-collection="):
			deps.TargetCollection = strings.TrimPrefix(a, "--target-collection=")
		default:
			if strings.HasPrefix(a, "-") {
				return deps, fmt.Errorf("unknown flag: %s", a)
			}
		}
	}
	if deps.Apply && deps.DryRun {
		return deps, fmt.Errorf("--apply and --dry-run are mutually exclusive")
	}
	return deps, nil
}

// runReindexQdrant is the entry point registered in cmd/admin/main.go.
//
// Pipeline:
//  1. Load config and open the media DB
//  2. Build the canonical Qdrant stack: SQLiteAssetStore → PayloadMapper → Client → IndexWriter
//  3. Dry-run: list all asset IDs via mapper.ListAllAssetIDs, print count
//  4. Apply: call IndexWriter.ReindexAll(ctx, targetCollection, limit)
func runReindexQdrant(args []string) error {
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	deps, err := parseReindexQdrantArgs(args)
	if err != nil {
		return err
	}

	if !cfg.Qdrant.Enabled {
		return errors.New(
			"qdrant is disabled in config (qdrant.enabled=false); " +
				"reindex-qdrant requires qdrant.enabled=true",
		)
	}

	ctx := cmdContext()

	log.Info("reindex-qdrant starting",
		zap.Bool("apply", deps.Apply),
		zap.Bool("dry_run", deps.DryRun || !deps.Apply),
		zap.Int("limit", deps.Limit),
		zap.String("target_collection", deps.TargetCollection),
		zap.String("qdrant_url", cfg.Qdrant.BaseURL),
	)

	sqliteDB, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open media DB: %w", err)
	}
	defer sqliteDB.Close()

	// Build canonical Qdrant stack.
	schema := qdrant.DefaultV3Schema()
	assetStore := qdrant.NewSQLiteAssetStore(sqliteDB.DB)
	mapper := qdrant.NewPayloadMapper(assetStore, log)
	client := qdrant.NewClient(&qdrant.Config{
		BaseURL: cfg.Qdrant.BaseURL,
		Timeout: cfg.Qdrant.Timeout,
	}, log)
	writer := qdrant.NewIndexWriter(client, schema, mapper, log)

	targetCollection := deps.TargetCollection
	if targetCollection == "" {
		targetCollection = schema.PhysicalName
	}

	// ── Dry-run ──────────────────────────────────────────────────
	if !deps.Apply {
		assetIDs, err := mapper.ListAllAssetIDs(ctx)
		if err != nil {
			return fmt.Errorf("list assets for dry-run: %w", err)
		}
		if deps.Limit > 0 && len(assetIDs) > deps.Limit {
			assetIDs = assetIDs[:deps.Limit]
		}

		result := map[string]any{
			"mode":              "dry-run",
			"target_collection": targetCollection,
			"total_assets":      len(assetIDs),
			"limit":             deps.Limit,
		}

		if deps.JSON {
			b, _ := json.Marshal(result)
			fmt.Println(string(b))
			return nil
		}

		log.Info("DRY-RUN complete (no Qdrant writes)",
			zap.Int("total_assets", len(assetIDs)),
			zap.String("target_collection", targetCollection))
		fmt.Printf("Dry-run: %d assets would be reindexed into %q\n", len(assetIDs), targetCollection)
		fmt.Println("Re-run with --apply to execute.")
		return nil
	}

	// ── Apply ────────────────────────────────────────────────────
	start := time.Now()
	reindexResult, err := writer.ReindexAll(ctx, targetCollection, deps.Limit)
	elapsed := time.Since(start)
	if err != nil {
		log.Error("reindex failed",
			zap.Int("indexed", reindexResult.IndexedAssets),
			zap.Int("failed", reindexResult.FailedAssets),
			zap.Error(err))
		return fmt.Errorf("reindex failed after %d indexed / %d failed: %w",
			reindexResult.IndexedAssets, reindexResult.FailedAssets, err)
	}

	if deps.JSON {
		b, _ := json.Marshal(reindexResult)
		fmt.Println(string(b))
		return nil
	}

	log.Info("reindex complete",
		zap.Int("total", reindexResult.TotalAssets),
		zap.Int("indexed", reindexResult.IndexedAssets),
		zap.Int("failed", reindexResult.FailedAssets),
		zap.String("collection", reindexResult.TargetCollection),
		zap.Duration("elapsed", elapsed))

	fmt.Printf("Reindex complete: %d indexed, %d failed (of %d total) into %q in %s\n",
		reindexResult.IndexedAssets,
		reindexResult.FailedAssets,
		reindexResult.TotalAssets,
		reindexResult.TargetCollection,
		elapsed.Round(time.Millisecond))
	return nil
}
