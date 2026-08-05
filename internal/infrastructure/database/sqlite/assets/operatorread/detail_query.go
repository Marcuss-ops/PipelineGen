package operatorread

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/operator"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func (r *InventoryReader) get(ctx context.Context, assetID string) (*operator.AssetInspection, error) {
	item, err := r.getItem(ctx, assetID)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, nil
	}

	locations, err := r.listLocations(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("operatorread.get locations: %w", err)
	}

	processing, err := r.listProcessing(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("operatorread.get processing: %w", err)
	}

	outboxEvents, err := r.listOutboxEvents(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("operatorread.get outbox events: %w", err)
	}

	metadata, err := r.loadMetadata(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("operatorread.get metadata: %w", err)
	}

	return &operator.AssetInspection{
		AssetInventoryItem: *item,
		Metadata:           metadata,
		Locations:          locations,
		Processing:         processing,
		OutboxEvents:       outboxEvents,
	}, nil
}

func (r *InventoryReader) getItem(ctx context.Context, assetID string) (*operator.AssetInventoryItem, error) {
	q := `WITH loc_flags AS (
    SELECT
        asset_id,
        MAX(CASE WHEN location_kind = 'local' THEN 1 ELSE 0 END) AS has_local,
        MAX(CASE WHEN location_kind = 'drive' THEN 1 ELSE 0 END) AS has_drive
    FROM asset_locations
    GROUP BY asset_id
),
outbox_counts AS (
    SELECT aggregate_id, COUNT(*) AS pending_events
    FROM outbox_events
    WHERE status = 'pending'
    GROUP BY aggregate_id
)
SELECT
    m.id,
    m.name,
    m.filename,
    m.source,
    m.provider,
    m.media_type,
    m.lifecycle_state,
    %s AS asset_state,
    m.index_state,
    m.file_hash AS content_hash,
    json_extract(COALESCE(m.metadata_json, '{}'), '$.indexed_content_hash') AS indexed_content_hash,
    json_extract(COALESCE(m.metadata_json, '{}'), '$.embedding_model_version') AS embedding_version,
    m.collection_version,
    COALESCE(loc.has_local, 0) AS has_local_file,
    COALESCE(loc.has_drive, 0) AS has_drive_file,
    CASE WHEN m.embedding_json IS NOT NULL AND m.embedding_json != '' AND m.embedding_json != '[]' THEN 1 ELSE 0 END AS has_embedding,
    COALESCE(o.pending_events, 0) AS pending_outbox_events,
    m.error AS last_error,
    m.created_at,
    m.updated_at
FROM media_assets m
LEFT JOIN loc_flags loc ON loc.asset_id = m.id
LEFT JOIN outbox_counts o ON o.aggregate_id = m.id
WHERE m.id = ? AND m.lifecycle_state != 'DELETED'`

	var item operator.AssetInventoryItem
	var lifecycleStr, assetStateStr, indexStateStr string
	var indexedHash, embeddingVersion sql.NullString
	var hasLocal, hasDrive, hasEmbedding int
	var createdAt, updatedAt string

	q = fmt.Sprintf(q, assetStateProjectionSQL("m"))
	err := r.db.QueryRowContext(ctx, q, assetID).Scan(
		&item.ID,
		&item.Name,
		&item.Filename,
		&item.Source,
		&item.Provider,
		&item.MediaType,
		&lifecycleStr,
		&assetStateStr,
		&indexStateStr,
		&item.ContentHash,
		&indexedHash,
		&embeddingVersion,
		&item.CollectionVersion,
		&hasLocal,
		&hasDrive,
		&hasEmbedding,
		&item.PendingOutboxEvents,
		&item.LastError,
		&createdAt,
		&updatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	item.LifecycleState = asset.LifecycleState(lifecycleStr)
	item.AssetState = asset.AssetState(assetStateStr)
	item.IndexState = asset.IndexState(indexStateStr)
	item.IndexedContentHash = indexedHash.String
	item.EmbeddingVersion = embeddingVersion.String
	item.HasLocalFile = hasLocal == 1
	item.HasDriveFile = hasDrive == 1
	item.HasEmbedding = hasEmbedding == 1
	item.IndexHealth = operator.ResolveIndexHealth(operator.IndexHealthInput{
		IndexState:          item.IndexState,
		ContentHash:         item.ContentHash,
		IndexedContentHash:  item.IndexedContentHash,
		PendingOutboxEvents: item.PendingOutboxEvents,
		LastError:           item.LastError,
	})
	item.CreatedAt = parseTime(createdAt)
	item.UpdatedAt = parseTime(updatedAt)

	return &item, nil
}

func (r *InventoryReader) listLocations(ctx context.Context, assetID string) ([]*asset.Location, error) {
	const q = `SELECT id, asset_id, location_kind, uri, external_id, web_view_link, download_url,
	       mime_type, file_size_bytes, file_hash, is_primary, created_at, updated_at
	FROM asset_locations
	WHERE asset_id = ?
	ORDER BY is_primary DESC, location_kind`

	rows, err := r.db.QueryContext(ctx, q, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*asset.Location
	for rows.Next() {
		var loc asset.Location
		var isPrimary int
		var createdAt, updatedAt string
		var errStr error
		errStr = rows.Scan(
			&loc.ID, &loc.AssetID, &loc.LocationKind, &loc.URI, &loc.ExternalID,
			&loc.AccessURL, &loc.DownloadURL, &loc.MimeType, &loc.FileSizeBytes,
			&loc.FileHash, &isPrimary, &createdAt, &updatedAt,
		)
		if errStr != nil {
			return nil, errStr
		}
		loc.IsPrimary = isPrimary == 1
		loc.CreatedAt = parseTime(createdAt)
		loc.UpdatedAt = parseTime(updatedAt)
		out = append(out, &loc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *InventoryReader) listProcessing(ctx context.Context, assetID string) ([]asset.ProcessingRecord, error) {
	const q = `SELECT asset_id, step, status, started_at, completed_at, error_message, attempt_count, metadata_json
	FROM asset_processing
	WHERE asset_id = ?
	ORDER BY updated_at DESC`

	rows, err := r.db.QueryContext(ctx, q, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []asset.ProcessingRecord
	for rows.Next() {
		var rec asset.ProcessingRecord
		var status, startedAt, completedAt, errMsg, meta string
		if err := rows.Scan(&rec.AssetID, &rec.Step, &status, &startedAt, &completedAt, &errMsg, &rec.AttemptCount, &meta); err != nil {
			return nil, err
		}
		rec.Status = asset.ProcessingStatus(status)
		if startedAt != "" {
			t := parseTime(startedAt)
			rec.StartedAt = &t
		}
		if completedAt != "" {
			t := parseTime(completedAt)
			rec.CompletedAt = &t
		}
		rec.ErrorMessage = errMsg
		rec.MetadataJSON = meta
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *InventoryReader) listOutboxEvents(ctx context.Context, assetID string) ([]operator.OutboxEventProjection, error) {
	const q = `SELECT event_type, aggregate_id, event_key, status, attempt_count, last_error, created_at, updated_at
	FROM outbox_events
	WHERE aggregate_id = ?
	ORDER BY created_at DESC
	LIMIT 50`

	rows, err := r.db.QueryContext(ctx, q, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []operator.OutboxEventProjection
	for rows.Next() {
		var ev operator.OutboxEventProjection
		var createdAt, updatedAt string
		if err := rows.Scan(&ev.EventType, &ev.AggregateID, &ev.EventKey, &ev.Status, &ev.AttemptCount, &ev.LastError, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		ev.CreatedAt = parseTime(createdAt)
		ev.UpdatedAt = parseTime(updatedAt)
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *InventoryReader) loadMetadata(ctx context.Context, assetID string) (map[string]any, error) {
	const q = `SELECT metadata_json FROM media_assets WHERE id = ?`
	var raw string
	if err := r.db.QueryRowContext(ctx, q, assetID).Scan(&raw); err != nil {
		return nil, err
	}
	if raw == "" || raw == "{}" || raw == "null" {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, err
	}
	return m, nil
}
