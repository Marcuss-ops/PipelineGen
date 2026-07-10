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
		var m map[string]interface{}
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
}

// canonicalQuery is the SELECT column list shared by FetchAsset and
// FetchAssetBatch. Column order MUST match assetRowScanner.scanArgs.
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
		created_at, updated_at, deleted_at
	FROM media_assets`

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
	return out, rows.Err()
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
