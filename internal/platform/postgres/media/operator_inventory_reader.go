// Package media — operator_inventory_reader.go: the operator console's
// Content Library / Asset Inspector / facet read model, answered from the
// PostgreSQL media SSOT.
//
// WHY THIS EXISTS. internal/platform/sqlite/assets/operatorread served this
// surface from the OPERATIONAL SQLite mirror (m.lifecycle_state,
// m.index_state, m.embedding_json, m.metadata_json ...) while the canonical
// committer wrote PostgreSQL. The operator console therefore graded a
// database that holds no post-cutover rows: a freshly committed asset was
// invisible to the Content Library, the Inspector reported pre-cutover
// state, and the facet counts described the mirror. This type is the
// 1:1 replacement, column for column, on the engine that owns media_assets.
//
// SEMANTIC PARITY, DELIBERATELY PRESERVED. The retired SQLite reader's
// contract is reproduced exactly:
//
//   - the same single-SELECT shape with the loc_flags / outbox_counts CTEs,
//     so there is no N+1 scan and no per-row round trip;
//   - `lifecycle_state != 'DELETED'` as the base filter, and the same
//     asset-state projection (lifecycle_state + index_state → asset_state),
//     because asset_state is a compatibility view and must not become
//     authoritative again;
//   - the same pagination contract (limit+1 look-ahead → HasMore, the
//     next cursor being the next offset);
//   - the same facet set and the same canonical-value merge, so every
//     canonical enum member keeps appearing with count 0;
//   - the same "not found" shape: a missing or soft-deleted asset resolves
//     to (nil, nil) in Get, never an error.
//
// ONE DELIBERATE DIVERGENCE, AND ONE THAT IS FORCED. The retired reader
// projected the SQLite `media_assets.error` column as `last_error`. That
// column does not exist on PostgreSQL at all (001_media_schema.sql declares
// no `error` on media_assets), so it cannot be projected here even for
// fidelity. The live index-error signal is the
// `metadata_json.$.last_index_error` key, which both engines genuinely
// maintain — pgmedia.SetIndexState jsonb_set/jsonb-removes it
// (mutations.go), and the SQLite committer surface does the same. The value
// is descriptive only (operator.ResolveIndexHealth uses it as the
// IndexHealthView description fallback), so the health CODE is unaffected.
//
// SECOND, SUBTLER DIVERGENCE. SQLite's LIKE is case-INsensitive for ASCII by
// default; PostgreSQL's LIKE is case-sensitive. A verbatim `LIKE` port would
// therefore have silently broken the Content Library search — an operator
// typing "beluga" would stop finding "Beluga underwater". The search arm
// uses ILIKE to preserve the retired observable behaviour.
package media

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/operator"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// OperatorInventoryReader is the PostgreSQL implementation of the operator
// read model. It is intentionally read-only and projects rows for the UI.
type OperatorInventoryReader struct {
	db  *sql.DB
	log *zap.Logger
}

// Compile-time assertion: the reader satisfies the canonical capability port.
// Drift on the interface is a build failure, not a runtime gap.
var _ operator.AssetInventoryReader = (*OperatorInventoryReader)(nil)

// NewOperatorInventoryReader constructs the read-only inventory reader on the
// PostgreSQL media database. db is required: a nil handle is a programming
// error, not a degrade path (wiring resolves it from the canonical media
// handle and leaves the port unwired when the media plane is closed).
func NewOperatorInventoryReader(db *sql.DB, log *zap.Logger) *OperatorInventoryReader {
	if db == nil {
		panic("media.NewOperatorInventoryReader: db is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &OperatorInventoryReader{db: db, log: log}
}

// DB exposes the underlying handle so callers can assert the reader lives on
// the media SSOT.
func (r *OperatorInventoryReader) DB() *sql.DB {
	if r == nil {
		return nil
	}
	return r.db
}

// List implements operator.AssetInventoryReader.
func (r *OperatorInventoryReader) List(ctx context.Context, query operator.AssetInventoryQuery) (operator.AssetInventoryPage, error) {
	return r.list(ctx, query)
}

// Get implements operator.AssetInventoryReader.
func (r *OperatorInventoryReader) Get(ctx context.Context, assetID string) (*operator.AssetInspection, error) {
	return r.get(ctx, assetID)
}

// Facets implements operator.AssetInventoryReader.
func (r *OperatorInventoryReader) Facets(ctx context.Context) (*operator.AssetInventoryFacets, error) {
	return r.facets(ctx)
}

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
// scanOperatorItems is the single scan site (the retired reader duplicated the
// projection across two files, which is exactly how they drift).
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

// operatorLocFlagsCTE and operatorOutboxCountsCTE are the two derived
// aggregates that keep the list a single SELECT. They read the media SSOT's
// own asset_locations / outbox_events tables, which live in the same
// PostgreSQL media database.
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
// offset as the cursor.
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
		// ILIKE, not LIKE: see the case-sensitivity note in the file header.
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

// scanOperatorItems is the single scan site for the item projection.
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

func (r *OperatorInventoryReader) facets(ctx context.Context) (*operator.AssetInventoryFacets, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("postgres media: operator inventory reader is not wired")
	}
	mediaTypes := map[string]int64{}
	lifecycleStates := map[string]int64{}
	assetStates := map[string]int64{}
	indexStates := map[string]int64{}
	sources := map[string]int64{}
	providers := map[string]int64{}

	queries := []struct {
		name      string
		keyColumn string
		out       map[string]int64
	}{
		{name: "media_type", keyColumn: "media_type", out: mediaTypes},
		{name: "lifecycle_state", keyColumn: "lifecycle_state", out: lifecycleStates},
		{name: "index_state", keyColumn: "index_state", out: indexStates},
		{name: "source", keyColumn: "source", out: sources},
		{name: "provider", keyColumn: "provider", out: providers},
	}

	for _, q := range queries {
		if err := r.runFacetQuery(ctx, q.name, q.keyColumn, q.out); err != nil {
			return nil, fmt.Errorf("operatorinventory.facets %s: %w", q.name, err)
		}
	}
	if err := r.runAssetStateFacetQuery(ctx, assetStates); err != nil {
		return nil, fmt.Errorf("operatorinventory.facets asset_state: %w", err)
	}

	return &operator.AssetInventoryFacets{
		MediaTypes:      mergeCanonicalFacet(mediaTypes, operator.MediaTypeLabels()),
		LifecycleStates: mergeCanonicalFacet(lifecycleStates, operator.LifecycleStateLabels()),
		AssetStates:     mergeCanonicalFacet(assetStates, operator.AssetStateLabels()),
		IndexStates:     mergeCanonicalFacet(indexStates, operator.IndexStateLabels()),
		Sources:         facetsFromMap(sources),
		Providers:       facetsFromMap(providers),
	}, nil
}

func (r *OperatorInventoryReader) runAssetStateFacetQuery(ctx context.Context, out map[string]int64) error {
	q := `SELECT ` + operatorAssetStateProjectionSQL("m") + ` AS k, COUNT(*) AS c
	FROM media_assets m WHERE m.lifecycle_state <> 'DELETED' GROUP BY k`
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var count int64
		if err := rows.Scan(&key, &count); err != nil {
			return err
		}
		out[key] = count
	}
	return rows.Err()
}

func (r *OperatorInventoryReader) runFacetQuery(ctx context.Context, name, keyColumn string, out map[string]int64) error {
	// keyColumn comes from the closed set above, never from caller input, so
	// interpolating it cannot become an injection surface.
	q := `SELECT COALESCE(` + keyColumn + `, '') AS k, COUNT(*) AS c
	FROM media_assets WHERE lifecycle_state <> 'DELETED' GROUP BY k`
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var key string
		var count int64
		if err := rows.Scan(&key, &count); err != nil {
			return err
		}
		// An empty source/provider is a real facet ("" = unset); an empty
		// canonical enum value is not a value at all.
		if key == "" && name != "source" && name != "provider" {
			continue
		}
		out[key] = count
	}
	return rows.Err()
}

// mergeCanonicalFacet ensures every canonical value appears in the facet
// group, even when the database count is zero. Labels are supplied by helpers
// in the domain package. The returned slice is sorted by code for stable
// output.
func mergeCanonicalFacet(counts map[string]int64, labels map[string]string) []operator.FacetGroup {
	out := make([]operator.FacetGroup, 0, len(labels))
	for code, label := range labels {
		out = append(out, operator.FacetGroup{
			Code:  code,
			Label: label,
			Count: counts[code],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

func facetsFromMap(m map[string]int64) []operator.FacetGroup {
	out := make([]operator.FacetGroup, 0, len(m))
	for code, count := range m {
		out = append(out, operator.FacetGroup{Code: code, Label: code, Count: count})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// parseOperatorTime parses the RFC3339 / naive-timestamp TEXT columns the
// media SSOT mirrors from SQLite. An unparseable or empty stamp yields the
// zero time, exactly as the retired SQLite read model did.
func parseOperatorTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return t
	}
	return time.Time{}
}
