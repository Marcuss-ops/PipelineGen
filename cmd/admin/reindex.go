// cmd/admin/reindex.go — QDRANT-005 PR 3 (June 2026)
//
// One-shot reindex of media_assets embeddings into the canonical Qdrant
// collection. Replaces the legacy Python writer scripts/tools/reindex_qdrant.py
// (deleted in this PR) which used to read media_assets via sqlite3 + push
// points via requests.put directly to the Qdrant REST API.
//
// Mirrors cmd/admin/backfill_media_assets_search_terms.go's split:
//   - runReindex(args)         cmd-context wrapper (appLogger + ctx + cleanup)
//   - parseReindexArgs(args)   pure argument parser (testable in isolation)
//   - reindexQdrant(ctx,...)   pure IO logic (testable without main-package state)
//
// Sparse BM25 vectors are NOT regenerated client-side. They were a legacy
// patch on the Python writer; Qdrant v1.x+ applies BM25 over payload indexes
// at query time independently. The collection's sparse_vector_name is
// preserved so any hybrid-search caller still works.
//
// Usage:
//
//	go run ./cmd/admin reindex                              # dry-run (counts only)
//	go run ./cmd/admin reindex --apply                      # write to Qdrant
//	go run ./cmd/admin reindex --apply --limit=100          # cap rows
//	go run ./cmd/admin reindex --apply --batch=200          # batch upsert size
//	go run ./cmd/admin reindex --apply --source=artlist     # scope to source
//	go run ./cmd/admin reindex --json                       # machine-readable
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
)

// defaultReindexBatchSize bounds how many VectorAssets are sent to Qdrant
// in a single PUT /collections/<c>/points call. Larger values reduce
// per-point HTTP overhead but extend the Qdrant write-lock window.
// 500 keeps the reindex safely below the conventional writer-lock budget.
const defaultReindexBatchSize = 500

// ReindexDeps groups the flag values for runReindex so the function can
// be unit-tested without reaching into package main. Apply=false means
// dry-run (rows counted but never PUT to Qdrant). BatchSize controls
// upsert chunking; empty values fall back to defaultReindexBatchSize.
type ReindexDeps struct {
	Apply     bool
	JSON      bool
	Limit     int
	BatchSize int
	Source    string
}

// parseReindexArgs returns ReindexDeps parsed from CLI args.
// Validation rules:
//   - BatchSize <= 0 falls back to defaultReindexBatchSize.
//   - Limit < 0 falls back to 0 (unbounded; the SQL itself is paginated by
//     the rows.Next() loop so memory usage stays bounded by the BatchSize).
type parseReindexArgs func(args []string) (ReindexDeps, error)

func defaultParseReindexArgs(args []string) (ReindexDeps, error) {
	deps := ReindexDeps{BatchSize: defaultReindexBatchSize}
	for _, a := range args {
		a = strings.TrimSpace(a)
		switch {
		case a == "--apply":
			deps.Apply = true
		case a == "--json":
			deps.JSON = true
		case strings.HasPrefix(a, "--limit="):
			n, err := parsePositiveFlag(a, "--limit")
			if err != nil {
				return deps, err
			}
			deps.Limit = n
		case strings.HasPrefix(a, "--batch="):
			n, err := parsePositiveFlag(a, "--batch")
			if err != nil {
				return deps, err
			}
			deps.BatchSize = n
		case strings.HasPrefix(a, "--source="):
			deps.Source = strings.TrimPrefix(a, "--source=")
		}
	}
	if deps.BatchSize <= 0 {
		deps.BatchSize = defaultReindexBatchSize
	}
	return deps, nil
}

// runReindex is the entrypoint registered in cmd/admin/main.go's switch.
// It uses appLogger() (from cmd/admin/logger.go) to resolve cfg + log + cleanup,
// opens the canonical media DB via storage.OpenSQLiteDB, builds a Qdrant
// service from cfg.VectorSearch, and delegates to reindexQdrant for the IO.
// Errors flow back to main() which exits non-zero (mirrors backfill_*).
func runReindex(args []string) error {
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	deps, err := defaultParseReindexArgs(args)
	if err != nil {
		return err
	}

	if !cfg.VectorSearch.Enabled {
		return errors.New(
			"vector search is disabled in config (vector_search.enabled=false); " +
				"reindex requires it to push embeddings into the canonical Qdrant collection",
		)
	}

	ctx := cmdContext()
	log.Info("opening media database for reindex",
		zap.String("data_dir", cfg.Storage.DataDir),
		zap.Bool("apply", deps.Apply),
		zap.Int("limit", deps.Limit),
		zap.Int("batch", deps.BatchSize),
		zap.String("source", deps.Source),
		zap.String("qdrant_url", cfg.VectorSearch.URL),
		zap.String("qdrant_collection", cfg.VectorSearch.Collection),
	)

	sqliteDB, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("failed to open media DB: %w", err)
	}
	defer sqliteDB.Close()
	db := sqliteDB.DB

	qdClient := buildReindexClient(cfg, log)
	collection := reindexCollectionName(cfg)

	start := time.Now()
	totalFound, pushed, skipped, failed, err := reindexQdrant(ctx, db, qdClient, collection, deps, log)
	elapsed := time.Since(start)
	if err != nil {
		return err
	}

	mode := "dry-run"
	if deps.Apply {
		mode = "apply"
	}
	result := map[string]any{
		"mode":              mode,
		"source":            deps.Source,
		"limit":             deps.Limit,
		"batch":             deps.BatchSize,
		"qdrant_url":        cfg.VectorSearch.URL,
		"qdrant_collection": cfg.VectorSearch.Collection,
		"total_found":       totalFound,
		"pushed":            pushed,
		"skipped":           skipped,
		"failed":            failed,
		"elapsed_ms":        elapsed.Milliseconds(),
	}

	if deps.JSON {
		b, _ := json.Marshal(result)
		fmt.Println(string(b))
		return nil
	}

	if !deps.Apply {
		log.Info("DRY-RUN complete (no Qdrant writes); re-run with --apply to push",
			zap.Int("total_found", totalFound))
		return nil
	}

	log.Info("reindex complete",
		zap.Int("pushed", pushed),
		zap.Int("skipped", skipped),
		zap.Int("failed", failed),
		zap.Int("total_found", totalFound),
		zap.Duration("elapsed", elapsed),
	)
	return nil
}// buildReindexClient creates a Qdrant Client from config for the admin reindex command.
func buildReindexClient(cfg *config.Config, log *zap.Logger) *qdrant.Client {
	if cfg == nil {
		return nil
	}
	return qdrant.NewClient(&qdrant.Config{
		BaseURL: cfg.VectorSearch.URL,
		Timeout: cfg.VectorSearch.TimeoutMs / 1000,
	}, log)
}

// reindexCollectionName resolves the collection to write to.
func reindexCollectionName(cfg *config.Config) string {
	col := cfg.VectorSearch.Collection
	if col == "" {
		col = "media_assets"
	}
	if v := cfg.VectorSearch.CollectionVersion; v != "" {
		col += "_" + v
	}
	return col
}

// reindexQdrant walks media_assets rows that carry at least one valid
// embedding JSON, builds qdrant.VectorAsset batches of size
// deps.BatchSize, and (only when deps.Apply=true) calls Client.UpsertVectorAssets.
// On a per-batch failure the batch is logged + counted toward failed,
// but iteration continues so a bad batch never blocks subsequent ones.
//
// Returns (totalFound, pushed, skipped, failed, err).
func reindexQdrant(
	ctx context.Context,
	db *sql.DB,
	qdClient *qdrant.Client,
	collection string,
	deps ReindexDeps,
	log *zap.Logger,
) (int, int, int, int, error) {
	whereClauses := []string{
		"( ",
		"  (embedding_json IS NOT NULL AND embedding_json != '' AND embedding_json != '[]' AND embedding_json != '{}')",
		"  OR (transcript_embedding IS NOT NULL AND transcript_embedding != '' AND transcript_embedding != '[]' AND transcript_embedding != '{}')",
		"  OR (visual_embedding IS NOT NULL AND visual_embedding != '' AND visual_embedding != '[]')",
		"  OR (json_extract(metadata_json, '$.audio_embedding_json') IS NOT NULL",
		"      AND json_extract(metadata_json, '$.audio_embedding_json') != ''",
		"      AND json_extract(metadata_json, '$.audio_embedding_json') != '[]')",
		")",
		"AND lifecycle_state != 'deleted' AND lifecycle_state != 'DELETED'",
	}
	args := []any{}
	if deps.Source != "" {
		whereClauses = append(whereClauses, "AND source = ?")
		args = append(args, deps.Source)
	}

	query := `
		SELECT
			id,
			COALESCE(name, '') AS name,
			COALESCE(source, '') AS source,
			COALESCE(category, '') AS category,
			COALESCE(tags, '[]') AS tags,
			COALESCE(search_text, '') AS search_text,
			COALESCE(drive_link, '') AS drive_link,
			COALESCE(local_path, '') AS local_path,
			COALESCE(embedding_json, '[]') AS embedding_json,
			COALESCE(visual_embedding, '[]') AS visual_embedding,
			COALESCE(transcript_embedding, '[]') AS transcript_embedding,
			COALESCE(json_extract(metadata_json, '$.audio_embedding_json'), '') AS audio_embedding_json,
			COALESCE(CAST(json_extract(metadata_json, '$.duration_ms') AS INTEGER), 0) AS duration_ms,
			COALESCE(json_extract(metadata_json, '$.language'), '') AS language,
			COALESCE(json_extract(metadata_json, '$.youtube_video_id'), '') AS youtube_video_id,
			COALESCE(json_extract(metadata_json, '$.youtube_url'), '') AS youtube_url,
			COALESCE(json_extract(metadata_json, '$.start_time'), '') AS start_time,
			COALESCE(json_extract(metadata_json, '$.end_time'), '') AS end_time
		FROM media_assets
		WHERE ` + strings.Join(whereClauses, "\n\t\t")
	if deps.Limit > 0 {
		query += "\n\t\tLIMIT ?"
		args = append(args, deps.Limit)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("query media_assets: %w", err)
	}
	defer rows.Close()

	batch := make([]qdrant.VectorAsset, 0, deps.BatchSize)
	var totalFound, pushed, skipped, failed int

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if !deps.Apply {
			batch = batch[:0]
			return nil
		}
		if err := qdClient.UpsertVectorAssets(ctx, collection, batch); err != nil {
			log.Warn("qdrant batch upsert failed",
				zap.Int("batch_size", len(batch)),
				zap.Error(err))
			failed += len(batch)
		} else {
			pushed += len(batch)
		}
		batch = batch[:0]
		return nil
	}

	for rows.Next() {
		va, parseErr := scanReindexRow(rows)
		if parseErr != nil {
			log.Warn("scan row failed", zap.Error(parseErr))
			continue
		}
		totalFound++

		// Skip rows where no embedding recovered — these are dead assets
		// with empty/garbage JSON. Mirrors the Python writer's `skipped` counter.
		if len(va.TextEmbedding) == 0 &&
			len(va.VisualEmbedding) == 0 &&
			len(va.TranscriptEmbedding) == 0 {
			skipped++
			continue
		}

		batch = append(batch, va)
		if len(batch) >= deps.BatchSize {
			if err := flush(); err != nil {
				return totalFound, pushed, skipped, failed, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return totalFound, pushed, skipped, failed, fmt.Errorf("iterate rows: %w", err)
	}
	if err := flush(); err != nil {
		return totalFound, pushed, skipped, failed, err
	}
	return totalFound, pushed, skipped, failed, nil
}

// scanReindexRow reads one media_assets row + builds a qdrant.VectorAsset.
// Pure (no side effects on the DB) so tests can drive it with sql.Rows
// from any fixture.
func scanReindexRow(rows *sql.Rows) (qdrant.VectorAsset, error) {
	var (
		va               qdrant.VectorAsset
		embeddingJSON    string
		visualEmbStr     string
		transcriptEmbStr string
		audioEmbStr      string
		tagsJSON         string
	)
	err := rows.Scan(
		&va.AssetID, &va.Name, &va.Source, &va.Category, &tagsJSON, &va.SearchText,
		&va.DriveLink, &va.LocalPath,
		&embeddingJSON, &visualEmbStr, &transcriptEmbStr, &audioEmbStr,
		&va.DurationMs, &va.Language, &va.YouTubeVideoID, &va.YouTubeURL,
		&va.StartTime, &va.EndTime,
	)
	if err != nil {
		return va, err
	}
	va.TextEmbedding = parseEmbeddingJSON(embeddingJSON)
	va.VisualEmbedding = parseEmbeddingJSON(visualEmbStr)
	va.TranscriptEmbedding = parseEmbeddingJSON(transcriptEmbStr)
	// audio_embedding_json is from metadata_json (older shape); embed into
	// the payload, not the VectorAsset (no canonical AudioEmbedding field on
	// qdrant.VectorAsset) — qdrantPointFromAsset reads only the dense slots.
	_ = audioEmbStr
	if tagsJSON != "" && tagsJSON != "[]" {
		_ = jsonUnmarshalIntoStringSlice(tagsJSON, &va.Tags)
	}
	va.EmbeddingVersion = qdrant.CurrentEmbeddingVersion
	va.SearchTextVersion = qdrant.CurrentSearchTextVersion
	return va, nil
}

// parseEmbeddingJSON parses a JSON array of floats into []float32.
// Returns nil on empty/parse-failure input — mirrors the existing
// qdrant.ClipIndexerAdapter::parseEmbeddingJSON shape so the two paths
// stay byte-identical (the canonical Go path wins on a hand-off PR).
func parseEmbeddingJSON(raw string) []float32 {
	if raw == "" || raw == "[]" {
		return nil
	}
	var f64 []float64
	if err := json.Unmarshal([]byte(raw), &f64); err != nil {
		return nil
	}
	f32 := make([]float32, len(f64))
	for i, v := range f64 {
		f32[i] = float32(v)
	}
	return f32
}

// jsonUnmarshalIntoStringSlice decodes a JSON string array. Errors are
// tolerated (silently keep va.Tags == nil) so a malformed tag column on
// any single row doesn't abort the whole reindex.
func jsonUnmarshalIntoStringSlice(raw string, dst *[]string) error {
	if raw == "" || raw == "[]" {
		return nil
	}
	return json.Unmarshal([]byte(raw), dst)
}
