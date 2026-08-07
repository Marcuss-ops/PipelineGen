// cmd/admin/backfill_asset_embeddings_db.go — SQL query helpers for the
// embedding backfill CLI (Task 5, July 2026).
//
// Split rationale (Commit E, July 2026): the canonical backfill command
// (backfill_asset_embeddings.go) owns the entry point, arg parsing,
// output formatting, and the SQL-backed candidate fetcher wiring. This
// sibling owns the 2 SQL-pure candidate-fetch functions:
//
//   - fetchEmbeddingCandidates: SELECT media_assets rows for the active
//     backfill, applying resume-anchor + source filter + OnlyMissing
//     filter + LIMIT ordering. Used in normal (forward) mode.
//   - fetchFailedCandidates: SELECT media_assets rows restricted to a
//     caller-supplied id set; same row shape as fetchEmbeddingCandidates
//     but with IN(...) clause for the --retry-failed mode.
//
// Commit F (August 2026): the reusable core moved to
// internal/application/indexing/backfill; the shared row/run shapes
// (backfill.Candidate, backfill.Deps, backfill.Checkpoint) now come from
// that package. Both helpers additionally select the content_hash
// expression so the core can build the deterministic event_key
// fingerprint without owning SQL.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - The media_assets column selected here MUST match the schema
//     declared in internal/infrastructure/database/sqlite/.../migrations.
//     If a column is added or removed there, both helpers + their
//     Scan() destructuring MUST be updated in lockstep — these
//     queries read production shape by SSOT contract, no inline schema
//     derived here.
//   - The CASE WHEN embedding_* IS NOT NULL AND ... != '[]' AND ... !=
//     '{}' truthiness idiom is preserved verbatim across both helpers.
//     The "empty blob ⇒ false" semantics IS the production contract
//     for the embedding channels (an empty JSON object/array is
//     indistinguishable from NULL for the backfill purpose).
//
// godlike/07 honest lock:
//   - The empty-id-list fast-path on fetchFailedCandidates returns
//     (nil, nil), NOT (nil, err) — the only valid empty-input probe
//     of this function. Any other empty-list code-path would crash
//     on the IN(...) join, so the early return is the canonical
//     "no-op when nothing to retry" pattern.
//
// Sibling-file constraint (Commit E user spec): this file lives in
// cmd/admin (package main), NOT in internal/infrastructure. The
// helpers are one-shot-CLI-only SQL queries. Promoting them to
// internal/infrastructure would force a typed-port interface that
// no other consumer uses — a "dead interface" anti-pattern per
// the PipelineGen architecture rules. Keep them here, period.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/indexing/backfill"
)

// fetchEmbeddingCandidates queries media_assets for assets that need
// embedding backfill. In --only-missing mode, only returns assets with
// at least one empty embedding column.
func fetchEmbeddingCandidates(
	ctx context.Context,
	db *sql.DB,
	deps backfill.Deps,
	cp *backfill.Checkpoint,
) ([]backfill.Candidate, error) {
	query := `
		SELECT id, COALESCE(source, ''), COALESCE(name, ''), COALESCE(media_type, ''),
		       COALESCE(local_path, ''),
		       COALESCE(json_extract(metadata_json, '$.content_hash'), json_extract(metadata_json, '$.file_hash'), file_hash, ''),
		       CASE WHEN embedding_json IS NOT NULL AND embedding_json != '' AND embedding_json != '[]' AND embedding_json != '{}' THEN 1 ELSE 0 END,
		       CASE WHEN transcript_embedding IS NOT NULL AND transcript_embedding != '' AND transcript_embedding != '[]' AND transcript_embedding != '{}' THEN 1 ELSE 0 END,
		       CASE WHEN visual_embedding IS NOT NULL AND visual_embedding != '' AND visual_embedding != '[]' AND visual_embedding != '{}' THEN 1 ELSE 0 END,
		       CASE WHEN audio_embedding IS NOT NULL AND audio_embedding != '' AND audio_embedding != '[]' AND audio_embedding != '{}' THEN 1 ELSE 0 END
		FROM media_assets
		WHERE media_type != 'folder'
		  AND (deleted_at IS NULL OR deleted_at = '')`

	var queryArgs []any

	// Resume: start after last processed ID.
	if cp != nil && cp.LastProcessedID != "" && deps.Resume {
		query += ` AND id > ?`
		queryArgs = append(queryArgs, cp.LastProcessedID)
	}

	// Source filter.
	if deps.Source != "" {
		query += ` AND source = ?`
		queryArgs = append(queryArgs, deps.Source)
	}

	query += ` ORDER BY id ASC`

	if deps.Limit > 0 {
		query += ` LIMIT ?`
		queryArgs = append(queryArgs, deps.Limit)
	}

	rows, err := db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("query embedding candidates: %w", err)
	}
	defer rows.Close()

	var out []backfill.Candidate
	for rows.Next() {
		var a backfill.Candidate
		var hasText, hasTranscript, hasVisual, hasAudio int
		if err := rows.Scan(&a.ID, &a.Source, &a.Name, &a.MediaType,
			&a.LocalPath, &a.ContentHash, &hasText, &hasTranscript, &hasVisual, &hasAudio); err != nil {
			return nil, fmt.Errorf("scan candidate: %w", err)
		}
		a.HasText = hasText == 1
		a.HasTranscript = hasTranscript == 1
		a.HasVisual = hasVisual == 1
		a.HasAudio = hasAudio == 1

		if deps.OnlyMissing && a.HasText && a.HasTranscript && a.HasVisual && a.HasAudio {
			continue // fully embedded, skip in --only-missing mode
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// fetchFailedCandidates returns candidate rows only for the given asset IDs.
func fetchFailedCandidates(ctx context.Context, db *sql.DB, ids []string) ([]backfill.Candidate, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT id, COALESCE(source, ''), COALESCE(name, ''), COALESCE(media_type, ''),
		       COALESCE(local_path, ''),
		       COALESCE(json_extract(metadata_json, '$.content_hash'), json_extract(metadata_json, '$.file_hash'), file_hash, ''),
		       CASE WHEN embedding_json IS NOT NULL AND embedding_json != '' AND embedding_json != '[]' AND embedding_json != '{}' THEN 1 ELSE 0 END,
		       CASE WHEN transcript_embedding IS NOT NULL AND transcript_embedding != '' AND transcript_embedding != '[]' AND transcript_embedding != '{}' THEN 1 ELSE 0 END,
		       CASE WHEN visual_embedding IS NOT NULL AND visual_embedding != '' AND visual_embedding != '[]' AND visual_embedding != '{}' THEN 1 ELSE 0 END,
		       CASE WHEN audio_embedding IS NOT NULL AND audio_embedding != '' AND audio_embedding != '[]' AND audio_embedding != '{}' THEN 1 ELSE 0 END
		FROM media_assets
		WHERE id IN (%s)
		  AND media_type != 'folder'
		  AND (deleted_at IS NULL OR deleted_at = '')
		ORDER BY id ASC`, strings.Join(placeholders, ","))

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query failed candidates: %w", err)
	}
	defer rows.Close()

	var out []backfill.Candidate
	for rows.Next() {
		var a backfill.Candidate
		var hasText, hasTranscript, hasVisual, hasAudio int
		if err := rows.Scan(&a.ID, &a.Source, &a.Name, &a.MediaType,
			&a.LocalPath, &a.ContentHash, &hasText, &hasTranscript, &hasVisual, &hasAudio); err != nil {
			return nil, fmt.Errorf("scan failed candidate: %w", err)
		}
		a.HasText = hasText == 1
		a.HasTranscript = hasTranscript == 1
		a.HasVisual = hasVisual == 1
		a.HasAudio = hasAudio == 1
		out = append(out, a)
	}
	return out, rows.Err()
}
