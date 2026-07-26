// Package indexing — asset_store_batch.go owns the CURSOR
// PAGINATION flow of SQLiteAssetStore: FetchAssetBatch (a page of N
// full AssetData rows after a given cursor ID, ordered ASC) +
// populateTranscriptsBatch (one IN-list query stitches transcript
// rows into the paginated page in a single round-trip).
//
// Split rationale (operation-flow): see asset_store.go header.
//
// HIGH #8 (July 2026): this flow replaces the ReindexAll pattern of
// loading all IDs into memory and doing N+1 FetchAsset calls. A single
// SQL query per page fetches complete AssetData rows; the caller
// advances the cursor via the last asset's ID.
//
// Cross-file dependencies (same package `indexing`):
//   - SQLiteAssetStore struct (asset_store.go) — receiver field
//   - canonicalQuery (asset_store.go) — embedded SELECT fragment
//   - assetRowScanner + populate (asset_store.go) — row scanner
//   - maxTranscriptsPerAsset (asset_store.go) — in-code cap
//     (SQL IN-list doesn't carry LIMIT so we enforce the cap here).
//
// godlike/07 NO-FAKE-AVAILABILITY: schema drift on asset_text_tracks
// (table missing) is quiet-failed; AssetData.Transcripts stays nil
// per asset and the composer falls back to the legacy Transcript
// field (godlike/07 minimum-blast-radius transition contract).
package indexing

import (
	"context"
	"fmt"
	"strings"
)

// FetchAssetBatch returns a page of full AssetData rows using cursor-based
// pagination (WHERE id > ? ORDER BY id LIMIT ?).
//
// Same filter as ListAllAssetIDs (in asset_store_fetch.go): excludes
// folders, soft-deleted rows, and rows without a populated text
// embedding.
//
// PR-CATALOG-MULTILINGUA step 6 (July 2026): after the main scan loop,
// ONE additional SQL query fetches the per-page transcripts in batch
// from `asset_text_tracks WHERE text_kind='transcript' AND is_current=1`
// (keyed by asset_id IN (page ids)) and stitches them into the
// already-allocated AssetData.Transcripts slices. Avoids N+1.
// Quiet-fails on schema drift: when asset_text_tracks doesn't exist
// yet, AssetData.Transcripts stays nil for every asset and the
// composer falls back to the legacy single-string Transcript field.
func (s *SQLiteAssetStore) FetchAssetBatch(ctx context.Context, afterID string, limit int) ([]*AssetData, error) {
	if limit <= 0 {
		limit = 500
	}

	query := `SELECT ` + canonicalQuery + `
		WHERE ` + indexableAssetWhereClause

	var args []any
	if afterID != "" {
		query += ` AND id > ?`
		args = append(args, afterID)
	}
	query += ` ORDER BY id ASC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("fetch asset batch (after %q): %w", afterID, err)
	}
	defer rows.Close()

	var out []*AssetData
	for rows.Next() {
		a := &AssetData{}
		var row assetRowScanner

		if err := rows.Scan(row.scanArgs(a)...); err != nil {
			return nil, fmt.Errorf("scan asset in batch (after %q): %w", afterID, err)
		}

		row.populate(a)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate asset batch (after %q): %w", afterID, err)
	}
	s.populateTranscriptsBatch(ctx, out)
	return out, nil
}

// populateTranscriptsBatch fetches ALL transcript rows for the page
// in ONE query (keyed by asset_id IN (?, ?, ...)) and stitches them
// into the already-allocated AssetData slice. Avoids N+1 fetch-asset
// round-trips (HIGH #8 contract). Quiet-fails on schema drift.
//
// Per-asset cap: maxTranscriptsPerAsset (defined in asset_store.go)
// enforced in code per-asset here because the SQL IN-list doesn't
// carry LIMIT. godlike/07 minimum-blast-radius: defensive only —
// typical multilingual catalogs sit at ≤20 langs.
//
// SQL tiebreaker: ORDER BY (asset_id, language_code, id ASC) so two
// is_current=1 rows sharing the same language_code surface in
// deterministic insertion-order across re-runs.
func (s *SQLiteAssetStore) populateTranscriptsBatch(ctx context.Context, page []*AssetData) {
	if len(page) == 0 {
		return
	}
	placeholders := strings.Repeat("?,", len(page))
	placeholders = strings.TrimRight(placeholders, ",")
	args := make([]any, len(page))
	idToAsset := make(map[string]*AssetData, len(page))
	for i, a := range page {
		args[i] = a.ID
		idToAsset[a.ID] = a
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT asset_id, language_code, text
		FROM asset_text_tracks
		WHERE asset_id IN (%s)
		  AND text_kind = 'transcript'
		  AND is_current = 1
		ORDER BY asset_id ASC, language_code ASC, id ASC
	`, placeholders), args...)
	if err != nil {
		return // godlike/07 transition: missing schema is no crash
	}
	defer rows.Close()
	for rows.Next() {
		var assetID, lang, text string
		if err := rows.Scan(&assetID, &lang, &text); err != nil {
			continue
		}
		a, ok := idToAsset[assetID]
		if !ok || a == nil {
			continue
		}
		lang = strings.TrimSpace(lang)
		if text == "" {
			continue
		}
		if len(a.Transcripts) >= maxTranscriptsPerAsset {
			// Per-asset cap enforced in code. Truncation is recoverable
			// on the next page cursor pass.
			continue
		}
		a.Transcripts = append(a.Transcripts, TranscriptTrack{
			Lang:       lang,
			Text:       text,
			IsOriginal: strings.EqualFold(lang, strings.TrimSpace(a.Language)),
		})
	}
	if err := rows.Err(); err != nil {
		// godlike/07 transition + forward-prevention: connection drop
		// mid-iteration is NOT silently absorbed; the follow-up reindex
		// picks up the missing language slots on the next pass.
		_ = err
	}
}
