// Package qdrant — SQLite-backed AssetStore implementation.
//
// Provides the concrete bridge between the media_assets table and the
// qdrant.IndexWriter. Reads embedding vectors stored as JSON in the
// embedding_json / transcript_embedding columns, plus all payload fields.
//
// This file imports database/sql and encoding/json — the only place in the
// qdrant package that touches the persistence layer. All other files depend
// on the abstract AssetStore interface.
package qdrant

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

// FetchAsset reads one row from media_assets and populates an AssetData.
func (s *SQLiteAssetStore) FetchAsset(ctx context.Context, assetID string) (*AssetData, error) {
	a := &AssetData{}

	var tagsJSON, metaJSON, textEmbJSON, transcriptEmbJSON, visualEmbJSON, audioEmbJSON sql.NullString
	var durationMs sql.NullInt64
	var sourceVersionStr sql.NullString
	var language, category, style, channelID, lic sql.NullString
	var workspaceID, youtubeVideoID, youtubeURL, startTime, endTime sql.NullString
	var createdAt, updatedAt, deletedAt sql.NullString

	err := s.db.QueryRowContext(ctx, `
		SELECT
			id, COALESCE(name, ''), COALESCE(source, ''), COALESCE(media_type, ''),
			COALESCE(status, 'ACTIVE'),
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
		FROM media_assets
		WHERE id = ?
	`, assetID).Scan(
		&a.ID, &a.Name, &a.Source, &a.MediaType,
		&a.Status,
		&tagsJSON,
		&a.SearchText,
		&a.DriveLink,
		&a.LocalPath,
		&metaJSON,
		&textEmbJSON,
		&transcriptEmbJSON,
		&visualEmbJSON,
		&audioEmbJSON,
		&language, &category, &style,
		&youtubeVideoID, &youtubeURL,
		&startTime, &endTime,
		&durationMs,
		&workspaceID, &channelID, &lic,
		&sourceVersionStr,
		&createdAt, &updatedAt, &deletedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("asset %q not found in media_assets", assetID)
		}
		return nil, fmt.Errorf("fetch asset %q: %w", assetID, err)
	}

	// Parse tags JSON.
	if tagsJSON.Valid && tagsJSON.String != "" && tagsJSON.String != "[]" {
		json.Unmarshal([]byte(tagsJSON.String), &a.Tags)
	}

	// Parse metadata JSON.
	a.MetadataJSON = metaJSON.String
	if a.MetadataJSON != "" && a.MetadataJSON != "{}" {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(a.MetadataJSON), &m); err == nil {
			a.Metadata = m
		}
	}

	// Parse embedding vectors.
	if textEmbJSON.Valid && textEmbJSON.String != "" && textEmbJSON.String != "[]" && textEmbJSON.String != "{}" {
		json.Unmarshal([]byte(textEmbJSON.String), &a.TextVector)
	}
	if transcriptEmbJSON.Valid && transcriptEmbJSON.String != "" && transcriptEmbJSON.String != "[]" && transcriptEmbJSON.String != "{}" {
		json.Unmarshal([]byte(transcriptEmbJSON.String), &a.TranscriptVector)
	}
	if visualEmbJSON.Valid && visualEmbJSON.String != "" && visualEmbJSON.String != "[]" && visualEmbJSON.String != "{}" {
		json.Unmarshal([]byte(visualEmbJSON.String), &a.VisualVector)
	}
	if audioEmbJSON.Valid && audioEmbJSON.String != "" && audioEmbJSON.String != "[]" && audioEmbJSON.String != "{}" {
		json.Unmarshal([]byte(audioEmbJSON.String), &a.AudioVector)
	}

	// Optional fields.
	if language.Valid {
		a.Language = language.String
	}
	if category.Valid {
		a.Category = category.String
	}
	if style.Valid {
		a.Style = style.String
	}
	if channelID.Valid {
		a.ChannelID = channelID.String
	}
	if lic.Valid {
		a.License = lic.String
	}
	if youtubeVideoID.Valid {
		a.YouTubeVideoID = youtubeVideoID.String
	}
	if youtubeURL.Valid {
		a.YouTubeURL = youtubeURL.String
	}
	if startTime.Valid {
		a.StartTime = startTime.String
	}
	if endTime.Valid {
		a.EndTime = endTime.String
	}
	if durationMs.Valid {
		a.DurationMs = durationMs.Int64
	}
	if workspaceID.Valid {
		a.WorkspaceID = workspaceID.String
	}
	if sourceVersionStr.Valid {
		a.SourceVersion = sourceVersionStr.String
	}
	if createdAt.Valid {
		a.CreatedAt = createdAt.String
	}
	if updatedAt.Valid {
		a.UpdatedAt = updatedAt.String
	}
	if deletedAt.Valid {
		a.DeletedAt = deletedAt.String
	}

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

// ListAssetsForReconcile returns the minimum asset payload needed by the
// admin-side `cmd/admin/reconcile_qdrant.go` reconcile dry-run.
//
// TODO 2 close-out (June 2026): replaces the QDRANT-005 placeholder
// stub with a real SELECT against media_assets.
//
// Selection rules:
//   - includeLifecycleStates empty → every row with lifecycle_state not
//     equal to 'DELETED' AND media_type != 'folder' (folder rows are
//     tree metadata, not Qdrant-indexed assets; including them would
//     produce spurious missing-classifications).
//   - includeLifecycleStates non-empty → exact match via IN (?, ?, ...)
//     on lifecycle_state, also excluding folders.
//
// Output: id, workspace_id, lifecycle_state (COALESCE → 'ACTIVE' when
// empty), file_hash, metadata_json, content_hash (JSON_EXTRACT with
// file_hash fallback). ORDER BY id ASC for deterministic diffs.
//
// SQL composition holds the canonical placeholder invariant: user-supplied
// states pass through QueryContext args only; the IN-list placeholder
// string is built from a fixed `?,?,...` alphabet via strings.Join.
func (s *SQLiteAssetStore) ListAssetsForReconcile(ctx context.Context, includeLifecycleStates []string) ([]AssetData, error) {
	const selectCols = `
		SELECT
			id,
			COALESCE(workspace_id, ''),
			COALESCE(NULLIF(lifecycle_state, ''), 'ACTIVE'),
			COALESCE(file_hash, ''),
			COALESCE(metadata_json, '{}'),
			COALESCE(JSON_EXTRACT(metadata_json, '$.content_hash'), file_hash, '')
		FROM media_assets
	`

	const baseFilter = `media_type != 'folder'`

	var (
		query string
		args  []any
	)
	if len(includeLifecycleStates) == 0 {
		query = selectCols + " WHERE " + baseFilter + `
			  AND (lifecycle_state IS NULL OR lifecycle_state = '' OR lifecycle_state != 'DELETED')
			ORDER BY id ASC
		`
	} else {
		placeholders := make([]string, len(includeLifecycleStates))
		for i, st := range includeLifecycleStates {
			placeholders[i] = "?"
			args = append(args, st)
		}
		query = fmt.Sprintf(selectCols+`
			WHERE %s AND lifecycle_state IN (%s)
			ORDER BY id ASC
		`, baseFilter, strings.Join(placeholders, ","))
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list assets for reconcile: %w", err)
	}
	defer rows.Close()

	var out []AssetData
	for rows.Next() {
		var (
			a           AssetData
			workspaceID string
			lifecycle   string
			fileHash    string
			metaJSON    string
			contentHash string
		)
		if err := rows.Scan(
			&a.ID,
			&workspaceID,
			&lifecycle,
			&fileHash,
			&metaJSON,
			&contentHash,
		); err != nil {
			return nil, fmt.Errorf("scan reconcile row %q: %w", a.ID, err)
		}
		a.WorkspaceID = workspaceID
		a.LifecycleState = lifecycle
		a.ContentHash = contentHash
		a.MetadataJSON = metaJSON
		// Best-effort metadata map hydration. When the column carries a
		// non-empty JSON object we hydrate the in-memory map so callers
		// (cmd/admin/reconcile_qdrant.go::reconcileReaderAdapter) can
		// read content_hash overrides without re-parsing metadata_json.
		// file_hash is mirrored into Metadata["file_hash"] only when not
		// already set by JSON (older rows used file_hash as the canonical
		// fingerprint pre-migration 059).
		switch {
		case metaJSON != "" && metaJSON != "{}":
			var m map[string]interface{}
			if jerr := json.Unmarshal([]byte(metaJSON), &m); jerr == nil && m != nil {
				a.Metadata = m
				if fileHash != "" {
					if _, alreadySet := a.Metadata["file_hash"]; !alreadySet {
						a.Metadata["file_hash"] = fileHash
					}
				}
			} else if fileHash != "" {
				a.Metadata = map[string]interface{}{"file_hash": fileHash}
			}
		case fileHash != "":
			a.Metadata = map[string]interface{}{"file_hash": fileHash}
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reconcile rows: %w", err)
	}
	return out, nil
}
