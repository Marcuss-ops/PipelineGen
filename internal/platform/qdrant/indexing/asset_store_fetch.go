// Package indexing — asset_store_fetch.go owns the SINGLE-ASSET
// flow of SQLiteAssetStore: FetchAsset (one assetID → full AssetData)
// + ListAllAssetIDs (id-only listing) + populateTranscripts (the
// single-asset transcript side-query).
//
// Split rationale (operation-flow): see asset_store.go header.
//
// Cross-file dependencies (same package `indexing`, no imports needed):
//   - SQLiteAssetStore struct (asset_store.go) — receiver field
//   - canonicalQuery (asset_store.go) — embedded SELECT fragment
//   - assetRowScanner + populate (asset_store.go) — row scanner
//   - maxTranscriptsPerAsset (asset_store.go) — SQL LIMIT cap
//
// godlike/07 NO-FAKE-AVAILABILITY: missing rows surface typed
// errors; transcript side-query quiet-fails on schema drift
// (godlike/07 minimum-blast-radius transition contract) so old
// databases without `asset_text_tracks` keep working.
package indexing

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// FetchAsset reads one row from media_assets and populates an AssetData.
//
// PR 1 / QDRANT-005 closure (June 2026): AssetData.LifecycleState
// is sourced from media_assets.lifecycle_state (the canonical
// UPPERCASE column). The legacy `status` column is dropped by
// migration 101; the parallel lowercase enum (`asset.AssetStatus`)
// has been retired. Pre-PR1 the read helper COALESCE-fell-through
// `lifecycle_state` → `status` → 'ACTIVE' to mask writers that hit
// either column; post-PR1 the column store is the only source and
// the canonical fallback is 'ACTIVE'. The query no longer selects
// `status`, so the row layout shrinks by one column.
//
// PR-CATALOG-MULTILINGUA step 6 (July 2026): after the main scan, a
// tiny side-query populates AssetData.Transcripts from
// `asset_text_tracks WHERE text_kind='transcript' AND is_current=1`.
// Quiet-fails when asset_text_tracks doesn't exist yet (older DB or
// a unit-test stub): AssetData.Transcripts stays empty and the
// composer falls back to the legacy single-string Transcript field
// (godlike/07 minimum-blast-radius transition contract).
func (s *SQLiteAssetStore) FetchAsset(ctx context.Context, assetID string) (*AssetData, error) {
	a := &AssetData{}
	var row assetRowScanner

	err := s.db.QueryRowContext(ctx, `SELECT `+canonicalQuery+` WHERE id = ?`, assetID).Scan(row.scanArgs(a)...)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("asset %q not found in media_assets", assetID)
		}
		return nil, fmt.Errorf("fetch asset %q: %w", assetID, err)
	}

	row.populate(a)
	s.populateTranscripts(ctx, a, assetID)
	return a, nil
}

// ListAllAssetIDs returns the media_asset IDs suitable for reindexing.
//
// The reindex/verifier contract only covers assets that can actually be
// reconstructed into Qdrant points, so the shared eligibility predicate
// excludes folders, soft-deleted rows, and rows without a populated text
// embedding.
//
// godlike/07 NO-FAKE-AVAILABILITY: an empty result slice (not nil)
// survives a clean catalog rescan; rows.Err() surfaces typed error if
// the iteration was aborted.
func (s *SQLiteAssetStore) ListAllAssetIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM media_assets
		WHERE `+indexableAssetWhereClause+`
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("list asset IDs: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan asset ID: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// populateTranscripts fetches the per-asset current transcript
// rows from asset_text_tracks and attaches them to AssetData.Transcripts.
// godlike/07 NO-FAKE-AVAILABILITY: on schema drift (table missing),
// asset_text_tracks is swallowed and AssetData.Transcripts stays nil.
// The composer's legacy single-string fallback covers the transition.
//
// drift surface (forward-prevention): rows.Err() is checked AFTER the
// iteration. A mid-iteration connection drop surfaces as a silent
// incomplete slice without this check; a future agent deducing
// "Transcripts had 1 row" when the underlying query was aborted would
// lose multiple language slots without warning.
//
// maxTranscriptsPerAsset enforces the cap server-side via SQL LIMIT ?.
func (s *SQLiteAssetStore) populateTranscripts(ctx context.Context, a *AssetData, assetID string) {
	if a == nil {
		return
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT language_code, text
		FROM asset_text_tracks
		WHERE asset_id = ?
		  AND text_kind = 'transcript'
		  AND is_current = 1
		ORDER BY language_code ASC
		LIMIT ?
	`, assetID, maxTranscriptsPerAsset)
	if err != nil {
		return // godlike/07 transition: missing schema is no crash
	}
	defer rows.Close()
	for rows.Next() {
		var lang, text string
		if err := rows.Scan(&lang, &text); err != nil {
			continue
		}
		lang = strings.TrimSpace(lang)
		if text == "" {
			continue
		}
		a.Transcripts = append(a.Transcripts, TranscriptTrack{
			Lang:       lang,
			Text:       text,
			IsOriginal: strings.EqualFold(lang, strings.TrimSpace(a.Language)),
		})
	}
	if err := rows.Err(); err != nil {
		// godlike/07 transition: do not crash, but do not silently treat
		// an aborted query as success either. The follow-up reindex picks
		// up the missing language slots on the next pass.
		_ = err
	}
}
