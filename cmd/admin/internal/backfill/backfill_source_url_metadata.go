// cmd/admin/backfill_source_url_metadata.go — source_url convergence backfill.
package backfill

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
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
	matched, updated, err := backfillSourceURLMetadataCanonical(ctx, root.DB, mutator, *limit)
	if err != nil {
		return err
	}
	fmt.Printf("backfill-source-url-metadata: matched=%d updated=%d (url column → metadata_json.$.source_url, non-image rows, idempotent)\n", matched, updated)
	return nil
}

type dbExecer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func backfillSourceURLMetadataCanonical(ctx context.Context, db dbExecer, mutator persistence.AssetMutator, limit int) (int, int, error) {
	if db == nil || mutator == nil {
		return 0, 0, fmt.Errorf("backfill-source-url-metadata: canonical asset mutator is required")
	}
	predicate := `COALESCE(media_type, '') <> 'image' AND TRIM(COALESCE(url, '')) <> '' AND json_extract(COALESCE(metadata_json, '{}'), '$.source_url') IS NULL`
	var matched int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_assets WHERE `+predicate).Scan(&matched); err != nil {
		return 0, 0, fmt.Errorf("backfill-source-url-metadata: count: %w", err)
	}
	query := `SELECT id, url FROM media_assets WHERE ` + predicate + ` ORDER BY id`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, 0, fmt.Errorf("backfill-source-url-metadata: candidates: %w", err)
	}
	type candidate struct {
		assetID   string
		sourceURL string
	}
	candidates := make([]candidate, 0)
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.assetID, &item.sourceURL); err != nil {
			rows.Close()
			return matched, 0, err
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return matched, 0, err
	}
	rows.Close()

	updated := 0
	for _, item := range candidates {
		patchJSONBytes, err := json.Marshal(map[string]string{"source_url": item.sourceURL})
		if err != nil {
			return matched, updated, fmt.Errorf("backfill-source-url-metadata: marshal %s: %w", item.assetID, err)
		}
		patchJSON := string(patchJSONBytes)
		if err := mutator.PatchAsset(ctx, persistence.AssetPatch{AssetID: item.assetID, MetadataPatchJSON: &patchJSON}); err != nil {
			return matched, updated, fmt.Errorf("backfill-source-url-metadata: patch %s: %w", item.assetID, err)
		}
		updated++
	}
	return matched, updated, nil
}
