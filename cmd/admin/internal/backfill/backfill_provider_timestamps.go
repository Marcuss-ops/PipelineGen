package backfill

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	sqlitemediaregistry "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/mediaregistry"
	"go.uber.org/zap"
)

// RunBackfillMediaAssetSources runs source, taxonomy, and content registry
// backfills owned by the canonical media registry service.
func RunBackfillMediaAssetSources(args []string) error {
	flags := flag.NewFlagSet("backfill-media-asset-sources", flag.ContinueOnError)
	apply := flags.Bool("apply", false, "write missing media_asset_sources rows")
	jsonOutput := flags.Bool("json", false, "emit the report as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	dbSet, err := cli.OpenDatabaseSet(cfg, log)
	if err != nil {
		return fmt.Errorf("open database set: %w", err)
	}
	defer dbSet.Close()
	resolver, err := sqlitemediaregistry.NewCanonicalIdentityResolver(dbSet.Primary.DB)
	if err != nil {
		return err
	}
	ctx := cli.CmdContext()
	var report, taxonomyReport, contentSHA256Report, contentLinkReport any
	if *apply {
		report, err = resolver.Backfill(ctx)
	} else {
		report, err = resolver.PreviewBackfill(ctx)
	}
	if err != nil {
		return err
	}
	if taxonomyReport, err = resolver.BackfillTaxonomy(ctx, *apply); err != nil {
		return err
	}
	if contentSHA256Report, err = resolver.BackfillContentSHA256(ctx, *apply); err != nil {
		return err
	}
	if contentLinkReport, err = resolver.BackfillContentLinks(ctx, *apply); err != nil {
		return err
	}
	if *jsonOutput {
		mode := "dry-run"
		if *apply {
			mode = "apply"
		}
		encoded, marshalErr := json.Marshal(map[string]any{
			"mode": mode, "source_report": report, "taxonomy_report": taxonomyReport,
			"content_sha256_report": contentSHA256Report, "content_link_report": contentLinkReport,
		})
		if marshalErr != nil {
			return marshalErr
		}
		fmt.Println(string(encoded))
		return nil
	}
	log.Info("canonical media asset source backfill complete", zap.Bool("apply", *apply), zap.Any("source_report", report), zap.Any("taxonomy_report", taxonomyReport), zap.Any("content_sha256_report", contentSHA256Report), zap.Any("content_link_report", contentLinkReport))
	return nil
}

// RunBackfillProviderTimestamps reconciles provider/timestamp metadata from
// canonical columns through the canonical AssetMutator.
func RunBackfillProviderTimestamps(args []string) error {
	fs := flag.NewFlagSet("backfill-provider-timestamps", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	limit := fs.Int("limit", 0, "Maximum number of rows to backfill; zero means all")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *limit < 0 {
		return fmt.Errorf("--limit must be non-negative")
	}
	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	root, _, rootCleanup, err := wiring.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("initialize composition: %w", err)
	}
	defer rootCleanup()
	if root == nil || root.DB == nil || root.CanonicalAssetWriter == nil {
		return fmt.Errorf("database and canonical asset writer are required")
	}
	mutator, ok := root.CanonicalAssetWriter.(persistence.AssetMutator)
	if !ok || mutator == nil {
		return fmt.Errorf("canonical asset mutator is not available")
	}
	matched, updated, err := backfillProviderTimestampsCanonical(ctx, root.DB, mutator, *limit)
	if err != nil {
		return err
	}
	fmt.Printf("backfill-provider-timestamps: matched=%d updated=%d (columns → metadata_json canonical keys, idempotent)\n", matched, updated)
	return nil
}

func backfillProviderTimestampsCanonical(ctx context.Context, db dbExecer, mutator persistence.AssetMutator, limit int) (int, int, error) {
	if db == nil || mutator == nil {
		return 0, 0, fmt.Errorf("backfill-provider-timestamps: canonical asset mutator is required")
	}
	updates := []struct{ key, predicate, valueExpr string }{
		{"source_provider", `TRIM(COALESCE(source_provider, '')) <> '' AND json_extract(COALESCE(metadata_json, '{}'), '$.source_provider') IS NULL`, `source_provider`},
		{"source_video_id", `TRIM(COALESCE(source_video_id, '')) <> '' AND json_extract(COALESCE(metadata_json, '{}'), '$.source_video_id') IS NULL`, `source_video_id`},
		{"start_sec", `COALESCE(start_ms, 0) <> 0 AND json_extract(COALESCE(metadata_json, '{}'), '$.start_sec') IS NULL`, `(COALESCE(start_ms, 0) / 1000.0)`},
		{"end_sec", `COALESCE(end_ms, 0) <> 0 AND json_extract(COALESCE(metadata_json, '{}'), '$.end_sec') IS NULL`, `(COALESCE(end_ms, 0) / 1000.0)`},
	}
	countQuery := `SELECT COUNT(*) FROM media_assets WHERE (` + updates[0].predicate + ` OR ` + updates[1].predicate + ` OR ` + updates[2].predicate + ` OR ` + updates[3].predicate + `)`
	var matched int
	if err := db.QueryRowContext(ctx, countQuery).Scan(&matched); err != nil {
		return 0, 0, fmt.Errorf("backfill-provider-timestamps: count: %w", err)
	}
	matchedIDs := make(map[string]struct{})
	updatedIDs := make(map[string]struct{})
	now := time.Now().UTC().Format(time.RFC3339)
	type candidate struct {
		assetID  string
		rawValue string
	}
	for _, update := range updates {
		query := `SELECT id, CAST(` + update.valueExpr + ` AS TEXT) FROM media_assets WHERE ` + update.predicate + ` ORDER BY id`
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			return len(matchedIDs), len(updatedIDs), fmt.Errorf("backfill-provider-timestamps: candidates %s: %w", update.key, err)
		}
		candidates := make([]candidate, 0)
		for rows.Next() {
			var item candidate
			if err := rows.Scan(&item.assetID, &item.rawValue); err != nil {
				rows.Close()
				return len(matchedIDs), len(updatedIDs), fmt.Errorf("backfill-provider-timestamps: scan %s: %w", update.key, err)
			}
			if _, exists := matchedIDs[item.assetID]; !exists {
				matchedIDs[item.assetID] = struct{}{}
			}
			if limit <= 0 || len(candidates) < limit {
				candidates = append(candidates, item)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return len(matchedIDs), len(updatedIDs), fmt.Errorf("backfill-provider-timestamps: iterate %s: %w", update.key, err)
		}
		rows.Close()

		for _, item := range candidates {
			var patchValue any = item.rawValue
			if update.key == "start_sec" || update.key == "end_sec" {
				patchValue, err = strconv.ParseFloat(item.rawValue, 64)
				if err != nil {
					return len(matchedIDs), len(updatedIDs), fmt.Errorf("backfill-provider-timestamps: parse %s for %s: %w", update.key, item.assetID, err)
				}
			}
			patchBytes, err := json.Marshal(map[string]any{update.key: patchValue})
			if err != nil {
				return len(matchedIDs), len(updatedIDs), fmt.Errorf("backfill-provider-timestamps: marshal %s for %s: %w", update.key, item.assetID, err)
			}
			patchJSON := string(patchBytes)
			if err := mutator.PatchAsset(ctx, persistence.AssetPatch{AssetID: item.assetID, MetadataPatchJSON: &patchJSON, UpdatedAt: &now}); err != nil {
				return len(matchedIDs), len(updatedIDs), fmt.Errorf("backfill-provider-timestamps: patch %s for %s: %w", update.key, item.assetID, err)
			}
			updatedIDs[item.assetID] = struct{}{}
		}
	}
	return len(matchedIDs), len(updatedIDs), nil
}
