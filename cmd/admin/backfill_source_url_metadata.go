// cmd/admin/backfill_source_url_metadata.go — source_url convergence
// backfill (Fase D).
//
// godlike/06 SSOT: Asset.SourceURL (media_assets.url column) is the
// canonical owner of the source URL. The metadata_json key "source_url"
// is a provenance mirror that legacy producers wrote before the url
// column existed, and that downstream consumers (Qdrant search text,
// FindBySourceURL dedup, youtube_asset_mapper round-trip) still read.
//
// This one-shot reconciliation copies url → metadata_json.$.source_url
// for rows where the canonical column is non-empty and the mirror is
// absent. It deliberately EXCLUDES image assets: for images the url
// column holds the canonicalized Drive web link while the metadata key
// preserves the original source URL (see ScanMediaAsset in
// internal/infrastructure/database/sqlite/assets/scan_helpers.go) — those
// two values are intentionally different and must not be merged.
//
// The command is idempotent (json_extract guard) and additive; it never
// overwrites an existing source_url key.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

// runBackfillSourceURLMetadata reconciles the source_url metadata mirror
// from the canonical url column. Non-image rows only; idempotent.
func runBackfillSourceURLMetadata(args []string) error {
	fs := flag.NewFlagSet("backfill-source-url-metadata", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	limit := fs.Int("limit", 0, "Maximum number of rows to backfill; zero means all")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *limit < 0 {
		return fmt.Errorf("--limit must be non-negative")
	}

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("initialize composition: %w", err)
	}
	defer rootCleanup()
	if root == nil || root.DB == nil {
		return fmt.Errorf("database is required")
	}

	matched, updated, err := backfillSourceURLMetadata(ctx, root.DB, *limit)
	if err != nil {
		return err
	}
	fmt.Printf("backfill-source-url-metadata: matched=%d updated=%d (url column → metadata_json.$.source_url, non-image rows, idempotent)\n", matched, updated)
	return nil
}

// dbExecer is the minimal query surface the backfill needs. SQLiteDB
// (the composition-root handle) embeds *sql.DB and satisfies it.
type dbExecer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// backfillSourceURLMetadata runs the reconciliation and returns
// (matched, updated, error). matched counts rows where url is non-empty
// and the source_url key is absent; updated counts rows actually patched.
func backfillSourceURLMetadata(ctx context.Context, db dbExecer, limit int) (int, int, error) {
	// COALESCE(media_type, '') <> 'image': legacy rows with a NULL
	// media_type are reconciled like any non-image row; image rows keep
	// their intentional field/key divergence (url column = Drive link,
	// metadata key = original source) and are excluded.
	//
	// SQLite does not support LIMIT on a bare UPDATE; the limit is applied
	// through an id-subquery so a bounded run stays portable.
	predicate := `
		  COALESCE(media_type, '') <> 'image'
		  AND TRIM(COALESCE(url, '')) <> ''
		  AND json_extract(COALESCE(metadata_json, '{}'), '$.source_url') IS NULL`
	nowStr := time.Now().UTC().Format(time.RFC3339)
	query := `UPDATE media_assets
		SET metadata_json = json_set(COALESCE(metadata_json, '{}'), '$.source_url', url),
		    updated_at = ?
		WHERE id IN (SELECT id FROM media_assets WHERE ` + predicate
	args := []any{nowStr}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	query += `)`

	// Match count first (same predicate, read-only) for the report.
	countQuery := `SELECT COUNT(*) FROM media_assets WHERE ` + predicate
	var matched int
	if err := db.QueryRowContext(ctx, countQuery).Scan(&matched); err != nil {
		return 0, 0, fmt.Errorf("backfill-source-url-metadata: count: %w", err)
	}

	res, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, 0, fmt.Errorf("backfill-source-url-metadata: update: %w", err)
	}
	updated, err := res.RowsAffected()
	if err != nil {
		return 0, 0, fmt.Errorf("backfill-source-url-metadata: rows affected: %w", err)
	}
	return matched, int(updated), nil
}
