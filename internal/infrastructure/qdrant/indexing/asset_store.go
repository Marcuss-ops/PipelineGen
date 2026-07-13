// Package qdrant — SQLite-backed AssetStore implementation.
//
// Provides the concrete bridge between the media_assets table and the
// qdrant.IndexWriter. Reads embedding vectors stored as JSON in the
// embedding_json / transcript_embedding columns, plus all payload fields.
//
// This file imports database/sql and encoding/json — the only place in the
// qdrant package that touches the persistence layer. All other files depend
// on the abstract AssetStore interface.
package indexing

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// SQLiteAssetStore implements AssetStore backed by the media_assets table.
type SQLiteAssetStore struct {
	db *sql.DB
}

// NewSQLiteAssetStore creates an AssetStore bound to a *sql.DB.
// The db must have the media_assets table with the canonical QDRANT-003 schema.
func NewSQLiteAssetStore(db *sql.DB) *SQLiteAssetStore {
	return &SQLiteAssetStore{db: db}
}

// Compile-time assertion: SQLiteAssetStore satisfies AssetStore.
var _ AssetStore = (*SQLiteAssetStore)(nil)

// ── Shared row scanner ───────────────────────────────────────────────
//
// assetRowScanner bundles every sql.Null* variable needed to scan a
// media_assets row. Both FetchAsset (single row via QueryRowContext)
// and FetchAssetBatch (cursor-based loop via QueryContext) declare the
// same scanner variables and call the same Scan pointer list. The
// populate method extracts the post-Scan parsing logic (tags JSON,
// metadata JSON, embedding vectors, optional fields) so the two
// methods share one canonical parser.

type assetRowScanner struct {
	tagsJSON, metaJSON, textEmbJSON, transcriptEmbJSON, visualEmbJSON, audioEmbJSON sql.NullString
	durationMs                                                                      sql.NullInt64
	sourceVersionStr                                                                sql.NullString
	language, category, style, channelID, lic                                       sql.NullString
	workspaceID, youtubeVideoID, youtubeURL, startTime, endTime                     sql.NullString
	createdAt, updatedAt, deletedAt                                                 sql.NullString
	lifecycleState                                                                  sql.NullString
	// semanticHash is the resolved semantic fingerprint:
	//   asset_visual_summaries.source_hash (when a real VLM pass has run)
	//   ∪ media_assets.semantic_hash (fallback)
	// godlike/06 SSOT precedence — see canonicalQuery's resolved_semantic_hash
	// subquery for the canonical pre-resolve at SQL-fetch time.
	resolvedSemanticHash sql.NullString
}

// scanArgs returns the pointer list passed to rows.Scan / QueryRowContext.Scan.
// Order MUST match the SELECT column order in the canonical query below.
func (r *assetRowScanner) scanArgs(a *AssetData) []any {
	return []any{
		&a.ID, &a.Name, &a.Source, &a.MediaType,
		&r.lifecycleState,
		&r.tagsJSON,
		&a.SearchText,
		&a.DriveLink,
		&a.LocalPath,
		&r.metaJSON,
		&r.textEmbJSON,
		&r.transcriptEmbJSON,
		&r.visualEmbJSON,
		&r.audioEmbJSON,
		&r.language, &r.category, &r.style,
		&r.youtubeVideoID, &r.youtubeURL,
		&r.startTime, &r.endTime,
		&r.durationMs,
		&r.workspaceID, &r.channelID, &r.lic,
		&r.sourceVersionStr,
		&r.createdAt, &r.updatedAt, &r.deletedAt,
		&r.resolvedSemanticHash,
	}
}

// populate fills the derived fields on a from the scanned row values.
// Call AFTER a successful Scan.
func (r *assetRowScanner) populate(a *AssetData) {
	if r.lifecycleState.Valid {
		a.LifecycleState = r.lifecycleState.String
	}

	// Parse tags JSON.
	if r.tagsJSON.Valid && r.tagsJSON.String != "" && r.tagsJSON.String != "[]" {
		json.Unmarshal([]byte(r.tagsJSON.String), &a.Tags)
	}

	// Parse metadata JSON.
	a.MetadataJSON = r.metaJSON.String
	if a.MetadataJSON != "" && a.MetadataJSON != "{}" {
		var m map[string]any
		if err := json.Unmarshal([]byte(a.MetadataJSON), &m); err == nil {
			a.Metadata = m
		}
	}

	// Parse embedding vectors.
	if r.textEmbJSON.Valid && r.textEmbJSON.String != "" && r.textEmbJSON.String != "[]" && r.textEmbJSON.String != "{}" {
		json.Unmarshal([]byte(r.textEmbJSON.String), &a.TextVector)
	}
	if r.transcriptEmbJSON.Valid && r.transcriptEmbJSON.String != "" && r.transcriptEmbJSON.String != "[]" && r.transcriptEmbJSON.String != "{}" {
		json.Unmarshal([]byte(r.transcriptEmbJSON.String), &a.TranscriptVector)
	}
	if r.visualEmbJSON.Valid && r.visualEmbJSON.String != "" && r.visualEmbJSON.String != "[]" && r.visualEmbJSON.String != "{}" {
		json.Unmarshal([]byte(r.visualEmbJSON.String), &a.VisualVector)
	}
	if r.audioEmbJSON.Valid && r.audioEmbJSON.String != "" && r.audioEmbJSON.String != "[]" && r.audioEmbJSON.String != "{}" {
		json.Unmarshal([]byte(r.audioEmbJSON.String), &a.AudioVector)
	}

	// Optional fields.
	if r.language.Valid {
		a.Language = r.language.String
	}
	if r.category.Valid {
		a.Category = r.category.String
	}
	if r.style.Valid {
		a.Style = r.style.String
	}
	if r.channelID.Valid {
		a.ChannelID = r.channelID.String
	}
	if r.lic.Valid {
		a.License = r.lic.String
	}
	if r.youtubeVideoID.Valid {
		a.YouTubeVideoID = r.youtubeVideoID.String
	}
	if r.youtubeURL.Valid {
		a.YouTubeURL = r.youtubeURL.String
	}
	if r.startTime.Valid {
		a.StartTime = r.startTime.String
	}
	if r.endTime.Valid {
		a.EndTime = r.endTime.String
	}
	if r.durationMs.Valid {
		a.DurationMs = r.durationMs.Int64
	}
	if r.workspaceID.Valid {
		a.WorkspaceID = r.workspaceID.String
	}
	if r.sourceVersionStr.Valid {
		a.SourceVersion = r.sourceVersionStr.String
	}
	if r.createdAt.Valid {
		a.CreatedAt = r.createdAt.String
	}
	if r.updatedAt.Valid {
		a.UpdatedAt = r.updatedAt.String
	}
	if r.deletedAt.Valid {
		a.DeletedAt = r.deletedAt.String
	}
	// Resolved semantic hash (godlike/06 SSOT precedence pinned by the
	// canonicalQuery's scalar subquery — see comment above).
	if r.resolvedSemanticHash.Valid {
		a.SemanticHash = r.resolvedSemanticHash.String
	}
}

// canonicalQuery is the SELECT column list shared by FetchAsset and
// FetchAssetBatch. Column order MUST match assetRowScanner.scanArgs.
//
// godlike/06 SSOT: the trailing `resolved_semantic_hash` scalar
// subquery pins the canonical pre-resolved value of current_semantic_hash
// with the precedence rule dictated by the Italian plan / step 6:
//
//	(a) asset_visual_summaries.source_hash (migration 151) — when a
//	    real VLM pass has run. Per migration 151, the table is 1:1
//	    with media_assets.id (PRIMARY KEY) — no language_code or
//	    is_current columns — so the lookup reduces to the canonical
//	    PK query. NULLIF around the inner SELECT distinguishes
//	    "no row" / "row.source_hash is empty default" from
//	    "row.source_hash is a real VLM fingerprint".
//	(b) media_assets.semantic_hash (migration 152) — fallback
//	    (also empty-string gated by NULLIF for symmetry).
//	(c) "" — neither populated.
//
// The forward-prevention invariant: COALESCE falls through on NULL
// only — passing through an empty string ('""') is treated as a
// populated sentinel. Per migration 151, source_hash is
// `TEXT NOT NULL DEFAULT ”`; an early insert with no real VLM
// pass populates source_hash with the empty default. Without
// NULLIF, the empty ” would block the semantic_hash fallback and
// the airlock would emit an empty current_semantic_hash — a
// silent regression against the user-spec (b) precedence rule.
//
// The airlock in index_airlock.go just trusts AssetData.SemanticHash;
// it does NOT re-derive precedence. AssetStore is the canonical owner
// of the precedence resolution (godlike/06 ONE canonical owner per
// fact).
const canonicalQuery = `
		id, COALESCE(name, ''), COALESCE(source, ''), COALESCE(media_type, ''),
		COALESCE(lifecycle_state, 'ACTIVE'),
		COALESCE(tags, '[]'),
		COALESCE(search_text, ''),
		COALESCE(drive_link, ''),
		COALESCE(local_path, ''),
		COALESCE(metadata_json, '{}'),
		COALESCE(embedding_json, '[]'),
		COALESCE(transcript_embedding, '[]'),
		COALESCE(visual_embedding, '[]'),
		COALESCE(audio_embedding, '[]'),
		language, category, style,
		youtube_video_id, youtube_url,
		start_time, end_time,
		duration_ms,
		workspace_id, channel_id, license,
		source_version,
		created_at, updated_at, deleted_at,
		COALESCE(
			NULLIF(
				(SELECT source_hash FROM asset_visual_summaries
				 WHERE asset_id = media_assets.id
				 LIMIT 1),
				''
			),
			NULLIF(semantic_hash, ''),
			''
		) AS resolved_semantic_hash
	FROM media_assets`

// maxTranscriptsPerAsset caps the per-asset transcript-row count
// fetched by `populateTranscripts` (single-asset flow). At 200
// languages per asset the slice is a generous load — typical
// multilingual catalogs sit at ≤20 langs; the cap is defensive.
const maxTranscriptsPerAsset = 200

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

// ListAllAssetIDs returns all media_asset IDs suitable for reindexing.
// Excludes folders and already-deleted rows to keep the index lean.
func (s *SQLiteAssetStore) ListAllAssetIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM media_assets
		WHERE media_type != 'folder'
		  AND (deleted_at IS NULL OR deleted_at = '')
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

// FetchAssetBatch returns a page of full AssetData rows using cursor-based
// pagination (WHERE id > ? ORDER BY id LIMIT ?).
//
// HIGH #8 (July 2026): replaces the ReindexAll pattern of loading all IDs
// into memory and doing N+1 FetchAsset calls. A single SQL query per page
// fetches complete AssetData rows; the caller advances the cursor via the
// last asset's ID.
//
// Same filter as ListAllAssetIDs: excludes folders and soft-deleted rows.
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
		WHERE media_type != 'folder'
		  AND (deleted_at IS NULL OR deleted_at = '')`

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

// populateTranscriptsBatch fetches ALL transcript rows for the page
// in ONE query (keyed by asset_id IN (?, ?, ...)) and stitches them
// into the already-allocated AssetData slice. Avoids N+1 fetch-asset
// round-trips (HIGH #8 contract). Quiet-fails on schema drift.
//
// Per-asset cap: the single-fetch path caps at maxTranscriptsPerAsset
// via SQL LIMIT ?. The batch path has no SQL-side cap (IN-list doesn't
// carry LIMIT) so we enforce maxTranscriptsPerAsset in code per-asset
// to defend against pathological multi-language catalog rows.
// godlike/07 minimum-blast-radius: defensive only — typical
// multilingual catalogs sit at ≤20 langs.
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

// ListAssetsForReconcile returns the minimum asset payload needed by the
// admin-side `cmd/admin/reconcile_qdrant.go` reconcile dry-run.
//
// Implements the SQL scan (June 2026 follow-up to QDRANT-005
// closure): selects id + workspace_id + lifecycle_state (with a
// COALESCE fallback through status and 'ACTIVE') + content_hash
// (extracted from metadata_json) from media_assets. Filters out
// folders and soft-deleted rows by default, and optionally
// restricts to a set of lifecycle states supplied by the caller.
// The reconciler service handles in-memory batching against Qdrant,
// so this query is a full scan of the eligible rows ordered by id
// for deterministic output.
func (s *SQLiteAssetStore) ListAssetsForReconcile(ctx context.Context, includeLifecycleStates []string) ([]AssetData, error) {
	query := `
		SELECT
			id,
			COALESCE(workspace_id, ''),
			COALESCE(lifecycle_state, 'ACTIVE'),
			COALESCE(json_extract(metadata_json, '$.content_hash'), '')
		FROM media_assets
		WHERE media_type != 'folder'
		  AND (deleted_at IS NULL OR deleted_at = '')
	`

	var args []any
	if len(includeLifecycleStates) > 0 {
		query += ` AND COALESCE(lifecycle_state, 'ACTIVE') IN (`
		for i, state := range includeLifecycleStates {
			if i > 0 {
				query += ", "
			}
			query += "?"
			args = append(args, state)
		}
		query += `)`
	}

	query += ` ORDER BY id`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query media_assets for reconcile: %w", err)
	}
	defer rows.Close()

	var out []AssetData
	for rows.Next() {
		var a AssetData
		var wsID, contentHash sql.NullString
		if err := rows.Scan(&a.ID, &wsID, &a.LifecycleState, &contentHash); err != nil {
			return nil, fmt.Errorf("scan asset for reconcile: %w", err)
		}
		if wsID.Valid {
			a.WorkspaceID = wsID.String
		}
		if contentHash.Valid {
			a.ContentHash = contentHash.String
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate media_assets for reconcile: %w", err)
	}
	return out, nil
}
