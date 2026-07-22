// cmd/admin/backfill_media_assets_search_terms.go — companion CLI (June 2026)
//
// One-shot backfill for media_assets rows that still hold the schema
// default `'[]'` (or `”` / NULL) for the `search_terms` JSON column.
//
// After the migration-091 companion code lands (DeriveSearchTerms +
// mergeSearchTerms wired into store.Save), every NEW ingest row
// auto-derives its search_terms. Existing rows that were inserted
// before that wiring aren't touched — this command is the migration
// path for legacy data.
//
// Usage:
//
//	go run ./cmd/admin backfill-media-assets-search-terms                  # dry-run
//	go run ./cmd/admin backfill-media-assets-search-terms --apply          # write
//	go run ./cmd/admin backfill-media-assets-search-terms --apply --limit=10000
//	go run ./cmd/admin backfill-media-assets-search-terms --apply --batch=2000
//	go run ./cmd/admin backfill-media-assets-search-terms --apply --source=artlist
//	go run ./cmd/admin backfill-media-assets-search-terms --json           # machine-readable
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
)

func runBackfillMediaAssetsSearchTerms(args []string) error {
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	deps, err := parseBackfillArgs(args)
	if err != nil {
		return err
	}

	ctx := cmdContext()
	log.Info("opening media database",
		zap.String("data_dir", cfg.Storage.DataDir),
		zap.Bool("apply", deps.Apply),
		zap.Int("limit", deps.Limit),
		zap.Int("batch", deps.BatchSize),
		zap.String("source", deps.Source))

	sqliteDB, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("failed to open media DB: %w", err)
	}
	defer sqliteDB.Close()
	db := sqliteDB.DB

	totalFound, updated, skipped, err := backfillMediaAssetsSearchTerms(ctx, db, deps, log)
	if err != nil {
		return err
	}

	if deps.JSON {
		mode := "dry-run"
		if deps.Apply {
			mode = "apply"
		}
		out := map[string]any{
			"mode":        mode,
			"source":      deps.Source,
			"limit":       deps.Limit,
			"batch":       deps.BatchSize,
			"total_found": totalFound,
			"updated":     updated,
			"skipped":     skipped,
		}
		b, _ := json.Marshal(out)
		fmt.Println(string(b))
		return nil
	}

	if !deps.Apply {
		log.Info("DRY-RUN complete (no rows updated); re-run with --apply to write",
			zap.Int("total_found", totalFound),
			zap.Int("limit", deps.Limit),
			zap.String("source", deps.Source))
		return nil
	}

	log.Info("backfill complete",
		zap.Int("updated", updated),
		zap.Int("skipped", skipped),
		zap.Int("total_found", totalFound),
		zap.Int("batch", deps.BatchSize))
	return nil
}

// ── Arg parsing (pure, testable) ─────────────────────────────────────────

// BackfillDeps groups the flag values for backfillMediaAssetsSearchTerms so
// the function can be unit-tested without reaching into package main.
// Apply=false means dry-run (rows counted but never UPDATEd). BatchSize
// controls tx-bound chunking on apply: rows are partitioned into windows
// of N, each window committed in its own transaction so a partial backfill
// never blocks the writer-lock for tens of seconds on 100K+ tables.
type BackfillDeps struct {
	Apply     bool
	JSON      bool
	Limit     int
	BatchSize int
	Source    string
}

// defaultBackfillBatchSize bounds how many UPDATEs run inside a single
// SQLite writer transaction. Larger values reduce disk-flush overhead
// but extend the writer-lock window. 5000 keeps the backfill below the
// conventional "interactive query" lock budget on a 100K-row table.
const defaultBackfillBatchSize = 5000

// parseBackfillArgs returns BackfillDeps parsed from CLI args.
// Validation failures populate deps.err (the cmd returns this verbatim).
// pendingMediaAssetRow is the row shape backfilledMediaAssetsSearchTerms
// reads from media_assets and applyBackfillBatch writes back. Defined at
// package level so applyBackfillBatch's signature can reference it (Go
// function-scoped types are not visible from sibling functions).
type pendingMediaAssetRow struct {
	id         string
	source     string
	name       string
	filename   string
	category   string
	tagsJSON   string
	searchText string
	// metadataJSON is consumed by applyBackfillBatch to hydrate
	// asset.Metadata before DeriveSearchTerms runs.
	metadataJSON string
}

func parseBackfillArgs(args []string) (BackfillDeps, error) {
	deps := BackfillDeps{BatchSize: defaultBackfillBatchSize}
	for _, a := range args {
		a = strings.TrimSpace(a)
		switch {
		case a == "--apply":
			deps.Apply = true
		case a == "--json":
			deps.JSON = true
		case strings.HasPrefix(a, "--limit="):
			n, err := cli.ParsePositiveFlag(a, "--limit")
			if err != nil {
				return deps, err
			}
			deps.Limit = n
		case strings.HasPrefix(a, "--batch="):
			n, err := cli.ParsePositiveFlag(a, "--batch")
			if err != nil {
				return deps, err
			}
			deps.BatchSize = n
		case strings.HasPrefix(a, "--source="):
			deps.Source = strings.TrimPrefix(a, "--source=")
		}
	}
	if deps.BatchSize <= 0 {
		deps.BatchSize = defaultBackfillBatchSize
	}
	return deps, nil
}

// ── Pure logic (testable, no main-package state) ─────────────────────────

func backfillMediaAssetsSearchTerms(ctx context.Context, db *sql.DB, deps BackfillDeps, log *zap.Logger) (int, int, int, error) {
	query := `
		SELECT id, source, name, filename, category, tags, search_text, metadata_json
		FROM media_assets
		WHERE search_terms = '[]' OR search_terms = '' OR search_terms IS NULL`
	var queryArgs []any
	if deps.Source != "" {
		query += " AND source = ?"
		queryArgs = append(queryArgs, deps.Source)
	}
	if deps.Limit > 0 {
		query += " LIMIT ?"
		queryArgs = append(queryArgs, deps.Limit)
	}

	rows, err := db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("query media_assets: %w", err)
	}
	defer rows.Close()

	var todo []pendingMediaAssetRow
	for rows.Next() {
		var p pendingMediaAssetRow
		if err := rows.Scan(&p.id, &p.source, &p.name, &p.filename, &p.category, &p.tagsJSON, &p.searchText, &p.metadataJSON); err != nil {
			log.Warn("scan row failed", zap.Error(err))
			continue
		}
		todo = append(todo, p)
	}
	if err := rows.Err(); err != nil {
		return 0, 0, 0, fmt.Errorf("iter rows: %w", err)
	}
	totalFound := len(todo)
	if totalFound == 0 {
		return 0, 0, 0, nil
	}
	if !deps.Apply {
		return totalFound, 0, 0, nil
	}

	updated, skipped := 0, 0
	for batchStart := 0; batchStart < len(todo); batchStart += deps.BatchSize {
		batchEnd := batchStart + deps.BatchSize
		if batchEnd > len(todo) {
			batchEnd = len(todo)
		}
		batch := todo[batchStart:batchEnd]
		batchUpdated, batchSkipped, err := applyBackfillBatch(ctx, db, batch, log)
		if err != nil {
			return totalFound, updated + batchUpdated, skipped + batchSkipped, fmt.Errorf("batch [%d:%d): %w", batchStart, batchEnd, err)
		}
		updated += batchUpdated
		skipped += batchSkipped
		log.Debug("backfill batch committed",
			zap.Int("batch_start", batchStart),
			zap.Int("batch_end", batchEnd),
			zap.Int("batch_size", len(batch)),
			zap.Int("updated", batchUpdated),
			zap.Int("skipped", batchSkipped))
	}
	return totalFound, updated, skipped, nil
}

// applyBackfillBatch runs one UPDATE per row inside a single transaction.
// BatchSize callers pin this so a partial failure mid-batch does not
// orphan inches of work between commits; one bad row falls through to
// skipped and the rest of the batch continues.
func applyBackfillBatch(ctx context.Context, db *sql.DB, batch []pendingMediaAssetRow, log *zap.Logger) (int, int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	updated, skipped := 0, 0
	for _, p := range batch {
		a := &asset.Asset{
			Source:     asset.Source(p.source),
			Name:       p.name,
			Filename:   p.filename,
			Category:   p.category,
			SearchText: p.searchText,
		}
		if p.tagsJSON != "" {
			var tags []string
			if err := json.Unmarshal([]byte(p.tagsJSON), &tags); err == nil {
				a.Tags = tags
			}
		}
		if p.metadataJSON != "" && p.metadataJSON != "{}" {
			var m map[string]any
			if err := json.Unmarshal([]byte(p.metadataJSON), &m); err == nil {
				a.Metadata = asset.Metadata(m)
			}
		}
		derived := asset.DeriveSearchTerms(a)
		termsJSON, err := json.Marshal(derived)
		if err != nil {
			log.Warn("marshal derived terms failed",
				zap.String("id", p.id), zap.Error(err))
			skipped++
			continue
		}
		if len(derived) == 0 {
			// Source field excluded + everything else blank = nothing to
			// derive. Leave the row at the schema default rather than
			// writing a token-empty '[]'. Surface in the skip counter so
			// operators see the gap in the log.
			skipped++
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE media_assets SET search_terms = ?, updated_at = datetime('now') WHERE id = ?`,
			string(termsJSON), p.id); err != nil {
			log.Warn("update search_terms failed",
				zap.String("id", p.id), zap.Error(err))
			skipped++
			continue
		}
		updated++
	}
	if err := tx.Commit(); err != nil {
		return updated, skipped, fmt.Errorf("commit: %w", err)
	}
	return updated, skipped, nil
}
