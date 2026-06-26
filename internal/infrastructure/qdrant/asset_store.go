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

// ReconcileSnapshot is the minimal fields the QDRANT-005B reconciler
// needs from media_assets. Defined here (not in the application
// reconciler package) so the SQL query can live next to its columns.
// The cmd/admin wire-up adapts []ReconcileSnapshot to
// reconciler.AssetSnapshot.
type ReconcileSnapshot struct {
	ID             string
	WorkspaceID    string
	LifecycleState string
	ContentHash    string
}

// ListAssetsForReconcile returns the snapshots the QDRANT-005B
// reconciler needs to scan. includeLifecycleStates restricts by
// media_assets.status when non-empty; empty means "do not restrict"
// (so DELETED rows are visible — important for verifying Qdrant
// points were cleaned up via DeleteEnqueued).
//
// Folders are always excluded: vector indexing of folders is
// meaningless and the previous ListAllAssetIDs pattern applies.
//
// Source_version is the canonical per-row content fingerprint the
// dispatcher uses as part of the event_key tuple (see
// outbox/repository.go::EnqueueAndIndex). For reconcile-repair
// events a stable, deterministic value is enough — the reconciler
// uses media_assets.source_version when available and falls back to
// a built-in reconcile-shaped string otherwise (see
// reconciler/service.go::applyRepair::lookupContentHash).
func (s *SQLiteAssetStore) ListAssetsForReconcile(ctx context.Context, includeLifecycleStates []string) ([]ReconcileSnapshot, error) {
	query := `
		SELECT id,
		       COALESCE(workspace_id, ''),
		       COALESCE(status, ''),
		       COALESCE(source_version, '')
		FROM media_assets
		WHERE media_type != 'folder'
	`
	var args []interface{}
	if len(includeLifecycleStates) > 0 {
		qmarks := make([]byte, 0, len(includeLifecycleStates)*2)
		for i, s := range includeLifecycleStates {
			if i > 0 {
				qmarks = append(qmarks, ',')
			}
			qmarks = append(qmarks, '?')
			args = append(args, s)
		}
		query += " AND status IN (" + string(qmarks) + ")"
	}
	query += " ORDER BY id"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list assets for reconcile: %w", err)
	}
	defer rows.Close()

	var out []ReconcileSnapshot
	for rows.Next() {
		var snap ReconcileSnapshot
		if err := rows.Scan(&snap.ID, &snap.WorkspaceID, &snap.LifecycleState, &snap.ContentHash); err != nil {
			return nil, fmt.Errorf("scan reconcile snapshot: %w", err)
		}
		out = append(out, snap)
	}
	return out, rows.Err()
}
