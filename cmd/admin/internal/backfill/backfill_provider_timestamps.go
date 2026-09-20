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
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
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
	// MEDIA-SSOT: the candidate scan reads media_assets, so it resolves from the
	// PostgreSQL media SSOT; the canonical patches already wrote there. The
	// operational handle is no longer needed for this command's reads.
	source := pgmedia.NewBackfillReader(root.MediaPostgres)
	if source == nil {
		return fmt.Errorf("media PostgreSQL SSOT is required for provider-timestamp backfill")
	}
	matched, updated, err := backfillProviderTimestampsCanonical(ctx, source, mutator, *limit)
	if err != nil {
		return err
	}
	fmt.Printf("backfill-provider-timestamps: matched=%d updated=%d (columns → metadata_json canonical keys, idempotent)\n", matched, updated)
	return nil
}

// providerTimestampSource is the narrow media read this backfill depends on.
//
// MEDIA-SSOT: production binds the PostgreSQL reader (pgmedia.BackfillReader).
// The rule SQL (which column feeds which metadata key) lives in that package,
// so this command names a rule instead of assembling dialect-specific SQL.
type providerTimestampSource interface {
	CountProviderTimestampCandidates(ctx context.Context) (int, error)
	ListProviderTimestampCandidates(ctx context.Context, key string) ([]pgmedia.ProviderTimestampCandidate, error)
}

func backfillProviderTimestampsCanonical(ctx context.Context, src providerTimestampSource, mutator persistence.AssetMutator, limit int) (int, int, error) {
	if src == nil || mutator == nil {
		return 0, 0, fmt.Errorf("backfill-provider-timestamps: canonical asset mutator is required")
	}
	matched, err := src.CountProviderTimestampCandidates(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("backfill-provider-timestamps: count: %w", err)
	}
	matchedIDs := make(map[string]struct{})
	updatedIDs := make(map[string]struct{})
	now := time.Now().UTC().Format(time.RFC3339)
	for _, key := range pgmedia.ProviderTimestampRuleKeys() {
		candidates, err := src.ListProviderTimestampCandidates(ctx, key)
		if err != nil {
			return len(matchedIDs), len(updatedIDs), fmt.Errorf("backfill-provider-timestamps: candidates %s: %w", key, err)
		}
		// The union is counted over EVERY matching row; --limit bounds only the
		// repaired set, exactly as the retired scan did.
		for _, item := range candidates {
			if _, exists := matchedIDs[item.AssetID]; !exists {
				matchedIDs[item.AssetID] = struct{}{}
			}
		}
		if limit > 0 && len(candidates) > limit {
			candidates = candidates[:limit]
		}

		for _, item := range candidates {
			var patchValue any = item.RawValue
			if key == "start_sec" || key == "end_sec" {
				patchValue, err = strconv.ParseFloat(item.RawValue, 64)
				if err != nil {
					return len(matchedIDs), len(updatedIDs), fmt.Errorf("backfill-provider-timestamps: parse %s for %s: %w", key, item.AssetID, err)
				}
			}
			patchBytes, err := json.Marshal(map[string]any{key: patchValue})
			if err != nil {
				return len(matchedIDs), len(updatedIDs), fmt.Errorf("backfill-provider-timestamps: marshal %s for %s: %w", key, item.AssetID, err)
			}
			patchJSON := string(patchBytes)
			if err := mutator.PatchAsset(ctx, persistence.AssetPatch{AssetID: item.AssetID, MetadataPatchJSON: &patchJSON, UpdatedAt: &now}); err != nil {
				return len(matchedIDs), len(updatedIDs), fmt.Errorf("backfill-provider-timestamps: patch %s for %s: %w", key, item.AssetID, err)
			}
			updatedIDs[item.AssetID] = struct{}{}
		}
	}
	// Fail closed on a contradiction instead of reporting it as progress: the
	// union count and the union of scanned IDs describe the same row set, so a
	// disagreement means the per-rule scan did not observe what the count did.
	if matched != len(matchedIDs) {
		return len(matchedIDs), len(updatedIDs), fmt.Errorf("backfill-provider-timestamps: candidate count %d disagrees with %d scanned rows", matched, len(matchedIDs))
	}
	return len(matchedIDs), len(updatedIDs), nil
}
