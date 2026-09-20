// Package media — operator_inventory_queries.go: every SELECT behind the
// operator inventory read model, its shared projection, and its single scan
// site. The port surface and the facet arm live in operator_inventory_reader.go.
//
// ONE PROJECTION, ONE SCAN SITE. operatorItemProjection is the SELECT list and
// scanOperatorItems is the only place that turns those columns into an
// AssetInventoryItem; the list arm and the detail arm both go through them. The
// retired SQLite reader duplicated its projection across list_query.go and
// detail_query.go, which is exactly how the two arms drift apart (a column
// added to one and not the other is a silently wrong row, not a compile error).
//
// Every query here is PostgreSQL-native but keeps the retired SQLite reader's
// semantics: the $N placeholders are assigned in the same order the conditions
// are appended, `lifecycle_state <> 'DELETED'` is the base filter, and the
// explicit ORDER BY makes each result deterministic (the retired form relied on
// the same clause, because the callers index into these slices).
package media

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/operator"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// operatorJSONText projects one metadata_json key as TEXT. It is the
// PostgreSQL counterpart of SQLite's
// json_extract(COALESCE(metadata_json,'{}'), '$.<key>'): NULLIF guards only
// the empty-string NOT NULL default, which is not valid JSON.
func operatorJSONText(column, key string) string {
	return "(NULLIF(" + column + ", '')::jsonb)->>'" + key + "'"
}

// operatorAssetStateProjectionSQL is the read-only compatibility projection of
// the two authoritative media-assets state dimensions. lifecycle_state owns
// lifecycle/deletion; index_state owns embedding and vector-index progress.
//
// The physical asset_state column remains only for older API consumers and is
// maintained by migration triggers. Operator reads derive the value here so
// stale compatibility data cannot become authoritative again. The expression
// is the retired SQLite reader's, verbatim.
//
// The order of the branches is load-bearing and must not be reshuffled: a
// DELETED lifecycle outranks a failed index (permanent, not retryable), a
// failed index outranks every progress state, and NOT_INDEXABLE resolves to
// UPLOADED before DISCOVERED is reached.
func operatorAssetStateProjectionSQL(alias string) string {
	return "CASE " +
		"WHEN " + alias + ".lifecycle_state IN ('DELETED', 'INDEX_DELETED') OR " + alias + ".index_state = 'DELETED' THEN 'FAILED_PERMANENT' " +
		"WHEN " + alias + ".index_state IN ('EMBEDDING_FAILED', 'INDEXING_FAILED') THEN 'FAILED_RETRYABLE' " +
		"WHEN " + alias + ".lifecycle_state IN ('STAGING', 'PREPARING', 'PROCESSING') THEN 'DISCOVERED' " +
		"WHEN " + alias + ".index_state = 'NOT_INDEXABLE' THEN 'UPLOADED' " +
		"WHEN " + alias + ".index_state = 'DISCOVERED' THEN 'DISCOVERED' " +
		"WHEN " + alias + ".index_state = 'EMBEDDING' THEN 'TRANSLATED' " +
		"WHEN " + alias + ".index_state IN ('EMBEDDED', 'INDEXING') THEN 'INDEX_PENDING' " +
		"WHEN " + alias + ".index_state = 'INDEXED' AND " + alias + ".lifecycle_state = 'ACTIVE' THEN 'READY' " +
		"WHEN " + alias + ".index_state = 'INDEXED' THEN 'INDEXED' " +
		"ELSE 'DISCOVERED' END"
}

// operatorItemProjection is the shared SELECT list for the list and detail
// arms. Both arms must project the same columns in the same order so
// scanOperatorItems is the single scan site.
func operatorItemProjection() string {
	return `
    m.id,
    m.name,
    m.filename,
    m.source,
    m.provider,
    m.media_type,
    m.lifecycle_state,
    ` + operatorAssetStateProjectionSQL("m") + ` AS asset_state,
    m.index_state,
    m.legacy_file_md5 AS content_hash,
    ` + operatorJSONText("m.metadata_json", "indexed_content_hash") + ` AS indexed_content_hash,
    ` + operatorJSONText("m.metadata_json", "embedding_model_version") + ` AS embedding_version,
    m.collection_version,
    COALESCE(loc.has_local, 0) AS has_local_file,
    COALESCE(loc.has_drive, 0) AS has_drive_file,
    CASE WHEN m.embedding_json <> '' AND m.embedding_json <> '[]' THEN 1 ELSE 0 END AS has_embedding,
    COALESCE(o.pending_events, 0) AS pending_outbox_events,
    ` + operatorJSONText("m.metadata_json", "last_index_error") + ` AS last_error,
    m.created_at,
    m.updated_at`
}

// operatorLocFlagsCTE is the two derived aggregates that keep the list a single
// SELECT. They read the media SSOT's own asset_locations / outbox_events tables,
// which live in the same PostgreSQL media database.
const operatorLocFlagsCTE = `WITH loc_flags AS (
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
`

// list returns a page of AssetInventoryItem projections matching the query.
//
// The query is one SELECT against media_assets plus the two CTEs, so no N+1
// scans are produced. Pagination uses the retired reader's contract: fetch
// limit+1 rows, report HasMore when the extra row arrives, and emit the next
// offset as the cursor. Total is the count BEFORE pagination, so the UI can
// render "showing 2 of 5" without a second round trip.
func (r *OperatorInventoryReader) list(ctx context.Context, query operator.AssetInventoryQuery) (operator.AssetInventoryPage, error) {
	if r == nil || r.db == nil {
		return operator.AssetInventoryPage{}, fmt.Errorf("postgres media: operator inventory reader is not wired")
	}
	if query.Limit <= 0 {
		query.Limit = 50
	}
	if query.Limit > 200 {
		query.Limit = 200
	}

	// args holds the filter bind values; $N placeholders are assigned in the
	// order the conditions are appended, exactly like the retired `?` form.
	args := []any{}
	bind := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	conds := []string{"m.lifecycle_state <> 'DELETED'"}

	if query.Source != "" {
		conds = append(conds, "m.source = "+bind(query.Source))
	}
	if query.Provider != "" {
		conds = append(conds, "m.provider = "+bind(query.Provider))
	}
	if query.MediaType != "" {
		conds = append(conds, "m.media_type = "+bind(query.MediaType))
	}
	if query.LifecycleState != "" {
		conds = append(conds, "m.lifecycle_state = "+bind(query.LifecycleState))
	}
	if query.AssetState != "" {
		conds = append(conds, operatorAssetStateProjectionSQL("m")+" = "+bind(query.AssetState))
	}
	if query.IndexState != "" {
		conds = append(conds, "m.index_state = "+bind(query.IndexState))
	}

	searchCond := "TRUE"
	if strings.TrimSpace(query.Search) != "" {
		// ILIKE, not LIKE: see the case-sensitivity note in
		// operator_inventory_reader.go's header.
		like := "%" + query.Search + "%"
		p1, p2, p3 := bind(like), bind(like), bind(like)
		searchCond = "(m.name ILIKE " + p1 + " OR m.filename ILIKE " + p2 + " OR m.search_text ILIKE " + p3 + ")"
	}

	where := strings.Join(append(conds, searchCond), " AND ")

	total, err := r.countList(ctx, where, args)
	if err != nil {
		return operator.AssetInventoryPage{}, fmt.Errorf("operatorinventory.list count: %w", err)
	}

	pageArgs := make([]any, len(args))
	copy(pageArgs, args)
	pageArgs = append(pageArgs, query.Limit+1, query.Offset)

	q := operatorLocFlagsCTE + `SELECT` + operatorItemProjection() + `
FROM media_assets m
LEFT JOIN loc_flags loc ON loc.asset_id = m.id
LEFT JOIN outbox_counts o ON o.aggregate_id = m.id
WHERE ` + where + `
ORDER BY m.updated_at DESC, m.id DESC
LIMIT ` + fmt.Sprintf("$%d OFFSET $%d", len(pageArgs)-1, len(pageArgs))

	rows, err := r.db.QueryContext(ctx, q, pageArgs...)
	if err != nil {
		return operator.AssetInventoryPage{}, fmt.Errorf("operatorinventory.list query: %w", err)
	}
	defer rows.Close()

	items, err := scanOperatorItems(rows)
	if err != nil {
		return operator.AssetInventoryPage{}, fmt.Errorf("operatorinventory.list scan: %w", err)
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

func (r *OperatorInventoryReader) countList(ctx context.Context, where string, args []any) (int64, error) {
	q := operatorLocFlagsCTE + `SELECT COUNT(*)
FROM media_assets m
LEFT JOIN loc_flags loc ON loc.asset_id = m.id
LEFT JOIN outbox_counts o ON o.aggregate_id = m.id
WHERE ` + where
	var total int64
	if err := r.db.QueryRowContext(ctx, q, args...).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

// scanOperatorItems is the single scan site for the item projection. It also
// derives the index health view, so list and detail can never disagree about
// what a state means.
func scanOperatorItems(rows *sql.Rows) ([]*operator.AssetInventoryItem, error) {
	var out []*operator.AssetInventoryItem
	for rows.Next() {
		item := &operator.AssetInventoryItem{}
		var lifecycleStr, assetStateStr, indexStateStr string
		var indexedHash, embeddingVersion, lastError sql.NullString
		var hasLocal, hasDrive, hasEmbedding int
		var createdAt, updatedAt string

		if err := rows.Scan(
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
			&lastError,
			&createdAt,
			&updatedAt,
		); err != nil {
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
		item.LastError = lastError.String

		item.IndexHealth = operator.ResolveIndexHealth(operator.IndexHealthInput{
			IndexState:          item.IndexState,
			ContentHash:         item.ContentHash,
			IndexedContentHash:  item.IndexedContentHash,
			PendingOutboxEvents: item.PendingOutboxEvents,
			LastError:           item.LastError,
		})

		item.CreatedAt = parseOperatorTime(createdAt)
		item.UpdatedAt = parseOperatorTime(updatedAt)

		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// get returns the full operator inspection for one asset, or (nil, nil) when
// the asset is absent or soft-deleted — never a fake match.
func (r *OperatorInventoryReader) get(ctx context.Context, assetID string) (*operator.AssetInspection, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("postgres media: operator inventory reader is not wired")
	}
	item, err := r.getItem(ctx, assetID)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, nil
	}

	locations, err := r.listLocations(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("operatorinventory.get locations: %w", err)
	}
	processing, err := r.listProcessing(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("operatorinventory.get processing: %w", err)
	}
	outboxEvents, err := r.listOutboxEvents(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("operatorinventory.get outbox events: %w", err)
	}
	metadata, err := r.loadMetadata(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("operatorinventory.get metadata: %w", err)
	}

	return &operator.AssetInspection{
		AssetInventoryItem: *item,
		Metadata:           metadata,
		Locations:          locations,
		Processing:         processing,
		OutboxEvents:       outboxEvents,
	}, nil
}

// getItem is the detail arm's row read. It reuses the list arm's projection and
// scan, so a column can never exist in one arm and not the other. A
// soft-deleted id yields nil, matching the list filter rather than reporting a
// phantom asset.
func (r *OperatorInventoryReader) getItem(ctx context.Context, assetID string) (*operator.AssetInventoryItem, error) {
	q := operatorLocFlagsCTE + `SELECT` + operatorItemProjection() + `
FROM media_assets m
LEFT JOIN loc_flags loc ON loc.asset_id = m.id
LEFT JOIN outbox_counts o ON o.aggregate_id = m.id
WHERE m.id = $1 AND m.lifecycle_state <> 'DELETED'`

	rows, err := r.db.QueryContext(ctx, q, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items, err := scanOperatorItems(rows)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	return items[0], nil
}

// listLocations reads the canonical location owner (asset_locations) directly —
// the retired reader's contract. is_primary is scanned as an integer because
// the column is SMALLINT on PostgreSQL (the SQLite sibling stored the same 0/1
// shape), and the ordering keeps the primary location first for the Inspector.
func (r *OperatorInventoryReader) listLocations(ctx context.Context, assetID string) ([]*asset.Location, error) {
	const q = `SELECT id, asset_id, location_kind, uri, external_id, web_view_link, download_url,
	       mime_type, file_size_bytes, legacy_file_md5, is_primary, created_at, updated_at
	FROM asset_locations
	WHERE asset_id = $1
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
		if err := rows.Scan(
			&loc.ID, &loc.AssetID, &loc.LocationKind, &loc.URI, &loc.ExternalID,
			&loc.AccessURL, &loc.DownloadURL, &loc.MimeType, &loc.FileSizeBytes,
			&loc.LegacyFileMD5, &isPrimary, &createdAt, &updatedAt,
		); err != nil {
			return nil, err
		}
		loc.IsPrimary = isPrimary == 1
		loc.CreatedAt = parseOperatorTime(createdAt)
		loc.UpdatedAt = parseOperatorTime(updatedAt)
		out = append(out, &loc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *OperatorInventoryReader) listProcessing(ctx context.Context, assetID string) ([]asset.ProcessingRecord, error) {
	const q = `SELECT asset_id, step, status, started_at, completed_at, error_message, attempt_count, metadata_json
	FROM asset_processing
	WHERE asset_id = $1
	ORDER BY updated_at DESC`

	rows, err := r.db.QueryContext(ctx, q, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []asset.ProcessingRecord
	for rows.Next() {
		var rec asset.ProcessingRecord
		var status, errMsg, meta string
		// started_at / completed_at are NULLABLE TEXT on the PostgreSQL
		// asset_processing table (the SQLite sibling declared them NOT NULL
		// DEFAULT ''), so a still-running step scans as NULL and must stay
		// distinguishable from the zero timestamp.
		var startedAt, completedAt sql.NullString
		if err := rows.Scan(&rec.AssetID, &rec.Step, &status, &startedAt, &completedAt, &errMsg, &rec.AttemptCount, &meta); err != nil {
			return nil, err
		}
		rec.Status = asset.ProcessingStatus(status)
		if startedAt.Valid && startedAt.String != "" {
			t := parseOperatorTime(startedAt.String)
			rec.StartedAt = &t
		}
		if completedAt.Valid && completedAt.String != "" {
			t := parseOperatorTime(completedAt.String)
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

// listOutboxEvents is bounded at 50 rows: the Inspector shows recent activity,
// and an asset with a pathological retry history must not be able to make the
// detail page arbitrarily expensive.
func (r *OperatorInventoryReader) listOutboxEvents(ctx context.Context, assetID string) ([]operator.OutboxEventProjection, error) {
	const q = `SELECT event_type, aggregate_id, event_key, status, attempt_count, last_error, created_at, updated_at
	FROM outbox_events
	WHERE aggregate_id = $1
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
		ev.CreatedAt = parseOperatorTime(createdAt)
		ev.UpdatedAt = parseOperatorTime(updatedAt)
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// loadMetadata surfaces metadata_json as a typed map for the Inspector's
// advanced tabs. An empty/default document is an EMPTY MAP, not an error: most
// assets legitimately carry no metadata.
func (r *OperatorInventoryReader) loadMetadata(ctx context.Context, assetID string) (map[string]any, error) {
	var raw sql.NullString
	if err := r.db.QueryRowContext(ctx, `SELECT metadata_json FROM media_assets WHERE id = $1`, assetID).Scan(&raw); err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(raw.String)
	if trimmed == "" || trimmed == "{}" || trimmed == "null" {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
		return nil, err
	}
	return m, nil
}
