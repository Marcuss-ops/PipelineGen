package operatorread

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/operator"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// list returns a page of AssetInventoryItem projections matching the query.
//
// The query is built as a single SELECT against media_assets with two
// derived CTEs (loc_flags, outbox_counts) so no N+1 scans are produced.
func (r *InventoryReader) list(ctx context.Context, query operator.AssetInventoryQuery) (operator.AssetInventoryPage, error) {
	if query.Limit <= 0 {
		query.Limit = 50
	}
	if query.Limit > 200 {
		query.Limit = 200
	}

	args := []any{}
	conds := []string{"m.lifecycle_state != 'DELETED'"}

	if query.Source != "" {
		conds = append(conds, "m.source = ?")
		args = append(args, query.Source)
	}
	if query.Provider != "" {
		conds = append(conds, "m.provider = ?")
		args = append(args, query.Provider)
	}
	if query.MediaType != "" {
		conds = append(conds, "m.media_type = ?")
		args = append(args, query.MediaType)
	}
	if query.LifecycleState != "" {
		conds = append(conds, "m.lifecycle_state = ?")
		args = append(args, query.LifecycleState)
	}
	if query.AssetState != "" {
		conds = append(conds, "m.asset_state = ?")
		args = append(args, query.AssetState)
	}
	if query.IndexState != "" {
		conds = append(conds, "m.index_state = ?")
		args = append(args, query.IndexState)
	}

	searchCond := "1=1"
	if strings.TrimSpace(query.Search) != "" {
		searchCond = "(m.name LIKE ? OR m.filename LIKE ? OR m.search_text LIKE ?)"
		like := "%" + query.Search + "%"
		args = append(args, like, like, like)
	}

	where := strings.Join(append(conds, searchCond), " AND ")

	total, err := r.countList(ctx, where, args)
	if err != nil {
		return operator.AssetInventoryPage{}, fmt.Errorf("operatorread.list count: %w", err)
	}

	pageArgs := make([]any, len(args))
	copy(pageArgs, args)
	pageArgs = append(pageArgs, query.Limit+1, query.Offset)

	q := listBaseSQL(where)
	rows, err := r.db.QueryContext(ctx, q, pageArgs...)
	if err != nil {
		return operator.AssetInventoryPage{}, fmt.Errorf("operatorread.list query: %w", err)
	}
	defer rows.Close()

	items, err := r.scanItems(rows)
	if err != nil {
		return operator.AssetInventoryPage{}, fmt.Errorf("operatorread.list scan: %w", err)
	}

	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}

	nextCursor := ""
	if hasMore {
		nextCursor = fmt.Sprintf("%d", query.Offset+query.Limit)
	}

	return operator.AssetInventoryPage{
		Items:      items,
		Total:      total,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

func (r *InventoryReader) countList(ctx context.Context, where string, args []any) (int64, error) {
	q := listCountSQL(where)
	var total int64
	if err := r.db.QueryRowContext(ctx, q, args...).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func listBaseSQL(where string) string {
	return fmt.Sprintf(`WITH loc_flags AS (
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
    m.asset_state,
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
WHERE %s
ORDER BY m.updated_at DESC, m.id DESC
LIMIT ? OFFSET ?`, where)
}

func listCountSQL(where string) string {
	return fmt.Sprintf(`WITH loc_flags AS (
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
SELECT COUNT(*)
FROM media_assets m
LEFT JOIN loc_flags loc ON loc.asset_id = m.id
LEFT JOIN outbox_counts o ON o.aggregate_id = m.id
WHERE %s`, where)
}

func (r *InventoryReader) scanItems(rows *sql.Rows) ([]*operator.AssetInventoryItem, error) {
	var out []*operator.AssetInventoryItem
	for rows.Next() {
		item := &operator.AssetInventoryItem{}
		var lifecycleStr, assetStateStr, indexStateStr string
		var indexedHash, embeddingVersion sql.NullString
		var hasLocal, hasDrive, hasEmbedding int
		var createdAt, updatedAt string

		err := rows.Scan(
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

		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
