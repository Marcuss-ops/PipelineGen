// cmd/admin/backfill_source_url_metadata.go — source_url convergence backfill.
package backfill

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// RunBackfillSourceURLMetadata reconciles the source_url metadata mirror
// from the canonical url column. Non-image rows only; idempotent.
func RunBackfillSourceURLMetadata(args []string) error {
	fs := flag.NewFlagSet("backfill-source-url-metadata", flag.ContinueOnError)
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
	if root == nil || root.DB == nil {
		return fmt.Errorf("database is required")
	}
	mutator, ok := root.CanonicalAssetWriter.(persistence.AssetMutator)
	if !ok || mutator == nil {
		return fmt.Errorf("canonical asset mutation committer is not available")
	}
	// MEDIA-SSOT: the candidate scan reads media_assets, so it resolves from the
	// PostgreSQL media SSOT. Reads and the canonical patches now agree on one
	// engine instead of scanning a mirror that holds no committed media rows.
	source := pgmedia.NewBackfillReader(root.MediaPostgres)
	if source == nil {
		return fmt.Errorf("media PostgreSQL SSOT is required for source_url backfill")
	}
	matched, updated, err := backfillSourceURLMetadataCanonical(ctx, source, mutator, *limit)
	if err != nil {
		return err
	}
	fmt.Printf("backfill-source-url-metadata: matched=%d updated=%d (url column → metadata_json.$.source_url, non-image rows, idempotent)\n", matched, updated)
	return nil
}

// sourceURLMetadataSource is the narrow media read this backfill depends on.
//
// MEDIA-SSOT: production binds the PostgreSQL reader
// (pgmedia.BackfillReader); the port is engine-specific so the candidate scan
// cannot drift back onto the operational SQLite mirror.
type sourceURLMetadataSource interface {
	CountSourceURLMetadataCandidates(ctx context.Context) (int, error)
	ListSourceURLMetadataCandidates(ctx context.Context, limit int) ([]pgmedia.SourceURLMetadataCandidate, error)
}

func backfillSourceURLMetadataCanonical(ctx context.Context, src sourceURLMetadataSource, mutator persistence.AssetMutator, limit int) (int, int, error) {
	if src == nil || mutator == nil {
		return 0, 0, fmt.Errorf("backfill-source-url-metadata: canonical asset mutator is required")
	}
	matched, err := src.CountSourceURLMetadataCandidates(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("backfill-source-url-metadata: count: %w", err)
	}
	candidates, err := src.ListSourceURLMetadataCandidates(ctx, limit)
	if err != nil {
		return 0, 0, fmt.Errorf("backfill-source-url-metadata: candidates: %w", err)
	}

	updated := 0
	for _, item := range candidates {
		patchJSONBytes, err := json.Marshal(map[string]string{"source_url": item.SourceURL})
		if err != nil {
			return matched, updated, fmt.Errorf("backfill-source-url-metadata: marshal %s: %w", item.AssetID, err)
		}
		patchJSON := string(patchJSONBytes)
		if err := mutator.PatchAsset(ctx, persistence.AssetPatch{AssetID: item.AssetID, MetadataPatchJSON: &patchJSON}); err != nil {
			return matched, updated, fmt.Errorf("backfill-source-url-metadata: patch %s: %w", item.AssetID, err)
		}
		updated++
	}
	return matched, updated, nil
}
