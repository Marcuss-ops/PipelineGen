// cmd/admin/backfill_provider_timestamps.go — provenance/timestamp
// key convergence backfill (source_provider / source_video_id /
// start_sec / end_sec).
//
// SSOT doctrine (same godlike/06 contract as the source_url
// convergence): the media_assets COLUMNS (source_provider,
// source_video_id, start_ms, end_ms — migration 152) are the
// canonical storage for provider provenance and the clip window.
// The metadata_json KEYS (source_provider, source_video_id,
// start_sec, end_sec) are the read surface used by the typed
// accessors (asset.MetadataSourceProvider/VideoID, StartSec(),
// EndSec()) and by Qdrant search-text / enrichment consumers.
//
// Legacy rows written before the convergence stamped only the
// video_id / clip_start_sec / clip_end_sec keys (or no key at
// all) — so the canonical accessors returned empty/0 even when
// the columns were populated. This one-shot reconciliation copies
// columns → canonical keys for rows where the key is absent.
//
// The command is idempotent (json_extract guards) and additive; it
// never overwrites an existing canonical key. start_ms/end_ms are
// stored as INTEGER milliseconds and are mirrored as float seconds
// under start_sec/end_sec to match the AssetRow / accessor
// contract. Image rows are NOT excluded here (unlike the source_url
// backfill): there is no intentional field/key divergence for these
// keys, so every media_type is reconciled uniformly.
package backfill

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	sqlitemediaregistry "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/mediaregistry"
	"go.uber.org/zap"
)

// runBackfillMediaAssetSources is kept with the existing provenance backfill
// commands so the admin composition package does not grow another command
// file. It is dry-run by default and derives only from durable columns.
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
	db, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open media database: %w", err)
	}
	defer db.Close()
	resolver, err := sqlitemediaregistry.NewCanonicalIdentityResolver(db.DB)
	if err != nil {
		return err
	}
	ctx := cli.CmdContext()
	var report any
	var taxonomyReport any
	var contentSHA256Report any
	var contentLinkReport any
	if *apply {
		report, err = resolver.Backfill(ctx)
	} else {
		report, err = resolver.PreviewBackfill(ctx)
	}
	if err != nil {
		return err
	}
	taxonomyReport, err = resolver.BackfillTaxonomy(ctx, *apply)
	if err != nil {
		return err
	}
	// Byte-identity reconstruction runs BEFORE content links so the digests
	// copied from file_hash (64-hex guard) feed the content_objects insert.
	contentSHA256Report, err = resolver.BackfillContentSHA256(ctx, *apply)
	if err != nil {
		return err
	}
	// Content links run last: the source backfill above has just registered
	// the provenance rows (with their digests), so the CAS link backfill can
	// fill the remaining media_asset_sources.content_sha256 and create the
	// missing content_objects rows before the Qdrant v4 rebuild.
	contentLinkReport, err = resolver.BackfillContentLinks(ctx, *apply)
	if err != nil {
		return err
	}
	if *jsonOutput {
		mode := "dry-run"
		if *apply {
			mode = "apply"
		}
		encoded, marshalErr := json.Marshal(map[string]any{
			"mode":                  mode,
			"source_report":         report,
			"taxonomy_report":       taxonomyReport,
			"content_sha256_report": contentSHA256Report,
			"content_link_report":   contentLinkReport,
		})
		if marshalErr != nil {
			return marshalErr
		}
		fmt.Println(string(encoded))
		return nil
	}
	log.Info("canonical media asset source backfill complete",
		zap.Bool("apply", *apply), zap.Any("source_report", report),
		zap.Any("taxonomy_report", taxonomyReport),
		zap.Any("content_sha256_report", contentSHA256Report),
		zap.Any("content_link_report", contentLinkReport))
	return nil
}

// runBackfillProviderTimestamps reconciles the provider/timestamp
// metadata mirror from the canonical columns. Idempotent.
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
	if root == nil || root.DB == nil {
		return fmt.Errorf("database is required")
	}

	matched, updated, err := backfillProviderTimestamps(ctx, root.DB, *limit)
	if err != nil {
		return err
	}
	fmt.Printf("backfill-provider-timestamps: matched=%d updated=%d (columns → metadata_json.$.source_provider/$.source_video_id/$.start_sec/$.end_sec, idempotent)\n", matched, updated)
	return nil
}

// backfillProviderTimestamps runs the reconciliation and returns
// (matched, updated, error). matched counts rows where at least one
// canonical key is absent while its column is populated; updated
// counts DISTINCT rows actually patched (matched_before −
// matched_after), so the report stays truthful under --limit.
//
// The reconciliation runs as FOUR guarded UPDATE statements, one per
// canonical key, each with its own AND predicate. A single json_set
// chain would write JSON nulls for keys whose column is empty on
// rows that matched a different predicate arm (json_set with a NULL
// value materialises {"key": null} — verified against SQLite) — so
// the per-key UPDATE keeps the operation strictly additive and
// null-free. SQLite does not support LIMIT on a bare UPDATE; the
// limit is applied through an id-subquery so a bounded run stays
// portable.
func backfillProviderTimestamps(ctx context.Context, db dbExecer, limit int) (int, int, error) {
	nowStr := time.Now().UTC().Format(time.RFC3339)

	// Four per-key predicates; each row may match several (one per
	// missing canonical key).
	updates := []struct {
		key       string
		predicate string
		setValue  string
	}{
		{
			key:       "source_provider",
			predicate: `TRIM(COALESCE(source_provider, '')) <> '' AND json_extract(COALESCE(metadata_json, '{}'), '$.source_provider') IS NULL`,
			setValue:  `source_provider`,
		},
		{
			key:       "source_video_id",
			predicate: `TRIM(COALESCE(source_video_id, '')) <> '' AND json_extract(COALESCE(metadata_json, '{}'), '$.source_video_id') IS NULL`,
			setValue:  `source_video_id`,
		},
		{
			key:       "start_sec",
			predicate: `COALESCE(start_ms, 0) <> 0 AND json_extract(COALESCE(metadata_json, '{}'), '$.start_sec') IS NULL`,
			// start_ms is INTEGER milliseconds; the canonical key is float seconds.
			setValue: `(COALESCE(start_ms, 0) / 1000.0)`,
		},
		{
			key:       "end_sec",
			predicate: `COALESCE(end_ms, 0) <> 0 AND json_extract(COALESCE(metadata_json, '{}'), '$.end_sec') IS NULL`,
			setValue:  `(COALESCE(end_ms, 0) / 1000.0)`,
		},
	}

	// Match count first (OR of all four predicates, read-only) for the report.
	countQuery := `SELECT COUNT(*) FROM media_assets WHERE (
		` + updates[0].predicate + ` OR ` + updates[1].predicate + ` OR ` + updates[2].predicate + ` OR ` + updates[3].predicate + `
	)`
	var matched int
	if err := db.QueryRowContext(ctx, countQuery).Scan(&matched); err != nil {
		return 0, 0, fmt.Errorf("backfill-provider-timestamps: count: %w", err)
	}

	for _, u := range updates {
		query := `UPDATE media_assets
			SET metadata_json = json_set(COALESCE(metadata_json, '{}'), '$.' || ?, ` + u.setValue + `),
			    updated_at = ?
			WHERE id IN (SELECT id FROM media_assets WHERE ` + u.predicate
		args := []any{u.key, nowStr}
		if limit > 0 {
			query += ` LIMIT ?`
			args = append(args, limit)
		}
		query += `)`
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			return 0, 0, fmt.Errorf("backfill-provider-timestamps: update %s: %w", u.key, err)
		}
	}

	// Distinct rows actually patched = matched_before − matched_after
	// (the residual OR-count after the updates).
	var remaining int
	if err := db.QueryRowContext(ctx, countQuery).Scan(&remaining); err != nil {
		return 0, 0, fmt.Errorf("backfill-provider-timestamps: post-count: %w", err)
	}
	updated := matched - remaining
	return matched, updated, nil
}
