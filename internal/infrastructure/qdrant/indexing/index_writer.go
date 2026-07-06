package indexing

import (
	"context"
	"fmt"
	"math"

	"go.uber.org/zap"

	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
)

// IndexWriter handles point upsert and deletion for Qdrant collections.
// It implements clipindexer.VectorStoreIndexer and
// outbox.VectorPointDeleter (PR 4, June 2026,
// refactor/single-qdrant-runtime — the previous local
// `qdrant.QdrantDeleter` interface was deleted in favour of the
// canonical application-layer port).
//
// SINGLE TRUTH (Blocco 4e, July 2026): IndexWriter is the ONLY
// code path that calls transport.Client.UpsertPoints / transport.Client.DeletePoints.
// All Qdrant writes MUST route through this type. The outbox
// (Dispatcher.EnqueueAndIndex → outbox event → IndexWriter) is
// the canonical write trigger; ReindexAll is the documented admin
// bypass for blue-green operations. No other type in the codebase
// may call transport.Client write methods directly.
//
// All writes go through the runtime alias so callers never need to know
// the physical collection name.
type IndexWriter struct {
	client    *transport.Client
	idxSchema *schema.IndexSchema
	mapper    *PayloadMapper
	log       *zap.Logger
}

// NewIndexWriter creates an IndexWriter.
//
// Compile-time assertions: IndexWriter satisfies the generic
// schema.IndexWriterPort (used by clipindexer) and the application-layer
// outbox.VectorPointDeleter port (consumed by IndexDeleteHandler).
// PR 4 consolidated the previously-duplicated `QdrantDeleter`
// interfaces (one in infra, one in outbox) into the single
// outbox.VectorPointDeleter port that lives in
// internal/application/jobs/outbox/ports.go per AGENTS.md Pattern 0.
var (
	_ schema.IndexWriterPort        = (*IndexWriter)(nil)
	_ jobsoutbox.VectorPointDeleter = (*IndexWriter)(nil)
)

// NewIndexWriter creates an IndexWriter.
//
// Parameter naming: `idxSchema` (NOT `schema`) to avoid shadowing the
// imported `github.com/.../infrastructure/qdrant/schema` package inside
// this function body. Renamed as part of the QDRANT-001 Check 2 fix
// (July 2026); the earlier `schema *schema.IndexSchema` parameter silently
// re-routed any future `schema.X` reference to a method call on the
// local variable. Callers pass positionally, so this rename is
// non-breaking at the call-site layer.
func NewIndexWriter(client *transport.Client, idxSchema *schema.IndexSchema, mapper *PayloadMapper, log *zap.Logger) *IndexWriter {
	return &IndexWriter{
		client:    client,
		idxSchema: idxSchema,
		mapper:    mapper,
		log:       log,
	}
}

// UpsertFromClip reads a single clip's data from the asset store and upserts it
// to Qdrant. Implements clipindexer.VectorStoreIndexer.
func (w *IndexWriter) UpsertFromClip(ctx context.Context, clipID string) error {
	return w.UpsertFromClips(ctx, []string{clipID})
}

// UpsertFromClips reads multiple clips and batch-upserts them.
// Implements clipindexer.VectorStoreIndexer.
//
// HIGH #4 (July 2026): partial failures now return a typed *transport.PartialUpsertError
// with per-asset phase (fetch/map/upsert), cause, and retryability. The
// previous implementation lost the original error and reduced failures to
// a count-only summary.
func (w *IndexWriter) UpsertFromClips(ctx context.Context, clipIDs []string) error {
	if len(clipIDs) == 0 {
		return nil
	}

	points := make([]schema.Point, 0, len(clipIDs))
	var failures []transport.AssetUpsertFailure

	for _, clipID := range clipIDs {
		asset, err := w.mapper.FetchAsset(ctx, clipID)
		if err != nil {
			w.log.Warn("failed to fetch asset for qdrant upsert",
				zap.String("asset_id", clipID),
				zap.Error(err))
			failures = append(failures, transport.AssetUpsertFailure{
				AssetID: clipID,
				Phase:   "fetch",
				Cause:   err,
			})
			continue
		}
		point, err := w.mapper.AssetToPoint(ctx, asset, w.idxSchema)
		if err != nil {
			w.log.Warn("failed to map asset to qdrant point",
				zap.String("asset_id", clipID),
				zap.Error(err))
			failures = append(failures, transport.AssetUpsertFailure{
				AssetID: clipID,
				Phase:   "map",
				Cause:   err,
			})
			continue
		}
		points = append(points, *point)
	}

	if len(points) == 0 {
		if len(failures) > 0 {
			// Build the typed error so callers can inspect per-failure
			// causes instead of parsing a flat count-only string.
			return newPartialUpsertError(nil, failures)
		}
		return nil
	}

	// PR 5 (June 2026, fix/qdrant-tenant-scope): write through the
	// runtime alias directly. Qdrant accepts an alias as the
	// collection name in PUT/POST /points requests and the
	// resulting write is atomic — no mid-flight alias-switch race,
	// no extra round-trip. Pre-PR5 the writer called
	// GetAliasTarget per upsert/delete batch, paying one HTTP call
	// AND opening a window where a blue-green switch could land the
	// batch in the wrong physical collection.
	//
	// GetAliasTarget's legitimate uses (admin reconcile, DR, ensure
	// schema, snapshot) are unaffected — only the writer hot path
	// is changed.
	if err := w.client.UpsertPoints(ctx, w.idxSchema.RuntimeAlias, points); err != nil {
		return fmt.Errorf("upsert %d points to %q: %w", len(points), w.idxSchema.RuntimeAlias, err)
	}

	w.log.Info("upserted points to qdrant",
		zap.Int("count", len(points)),
		zap.String("collection", w.idxSchema.RuntimeAlias))

	if len(failures) > 0 {
		successIDs := make([]string, 0, len(points))
		for _, p := range points {
			// Extract the canonical media_assets.id from the
			// payload (PayloadMapper always sets asset_id).
			// Fall back to the Qdrant point ID only when the
			// payload is missing — the canonical path never
			// hits this branch.
			assetID := p.ID
			if p.Payload != nil {
				if id, ok := p.Payload["asset_id"].(string); ok && id != "" {
					assetID = id
				}
			}
			successIDs = append(successIDs, assetID)
		}
		return newPartialUpsertError(successIDs, failures)
	}
	return nil
}

// newPartialUpsertError constructs a *transport.PartialUpsertError and pre-computes
// Retryable by classifying each failure through the canonical
// qdrant.transport.IsRetryable helper. Centralised here so the retry decision is
// made once at construction time rather than re-derived by every caller.
func newPartialUpsertError(successIDs []string, failures []transport.AssetUpsertFailure) *transport.PartialUpsertError {
	retryable := false
	for i := range failures {
		if transport.IsRetryable(failures[i].Cause) {
			retryable = true
			break
		}
	}
	return &transport.PartialUpsertError{
		SuccessfulIDs: successIDs,
		Failures:      failures,
		Retryable:     retryable,
	}
}

// DeletePoints deletes points from the active collection by asset ID.
// Implements outbox.VectorPointDeleter (PR 4).
//
// QDRANT-001 closure (June 2026): each asset ID is canonicalised via
// schema.AssetIDToQdrantPointID before being sent to the Qdrant client. The
// `transport.Client.DeletePoints` is intentionally linear (it does NOT translate
// the IDs) — that contract is split here so the transport.Client layer stays
// Qdrant-native and free of asset-domain knowledge. Mirrors the
// PayloadMapper.AssetToPoint write path so any asset ID passed in
// produces a 1-to-1 delete against the prefix-namespaced schema.Point ID
// that the mapper originally wrote.
func (w *IndexWriter) DeletePoints(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	// Canonicalise ids → Qdrant point IDs. Empty inputs become empty
	// strings; we pass them through unchanged so the Qdrant API
	// (which treats an empty point-id as a no-op) keeps its current
	// semantics in legacy callers that haven't yet trimmed.
	pointIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		if pid := schema.AssetIDToQdrantPointID(id); pid != "" {
			pointIDs = append(pointIDs, pid)
		}
	}
	if len(pointIDs) == 0 {
		return nil
	}

	// PR 5 (June 2026, fix/qdrant-tenant-scope): same write-through-
	// alias rationale as UpsertFromClips above. The previous
	// alias-resolution round-trip is dropped; the alias name is
	// used directly as the Qdrant collection name in the delete
	// payload.
	if err := w.client.DeletePoints(ctx, w.idxSchema.RuntimeAlias, pointIDs); err != nil {
		return fmt.Errorf("delete points from %q: %w", w.idxSchema.RuntimeAlias, err)
	}

	w.log.Info("deleted points from qdrant",
		zap.Int("count", len(pointIDs)),
		zap.String("collection", w.idxSchema.RuntimeAlias))
	return nil
}

// ReindexAll reads all assets from the mapper's store via paginated
// cursor scan and upserts them into the given target collection.
//
// HIGH #8 (July 2026): replaced total-ID-in-memory + N+1 FetchAsset
// with cursor-based paginated scanning (WHERE id > ? ORDER BY id
// LIMIT 500). Each batch is fetched in a single SQL query; the
// writer receives complete batches without re-reading.
func (w *IndexWriter) ReindexAll(ctx context.Context, targetCollection string, limit int) (*schema.ReindexResult, error) {
	if targetCollection == "" {
		targetCollection = w.idxSchema.CanonicalName()
	}

	const pageSize = 500

	result := &schema.ReindexResult{
		TargetCollection: targetCollection,
	}

	var afterID string // cursor: tracks the last asset ID from the previous page
	var totalSeen int

	points := make([]schema.Point, 0, 100)
	var batchCount int

	for {
		// Respect the limit: if we've seen enough, stop.
		if limit > 0 && totalSeen >= limit {
			break
		}

		// Fetch one page of full AssetData rows.
		pageLimit := pageSize
		if limit > 0 && totalSeen+pageLimit > limit {
			pageLimit = limit - totalSeen
		}

		batch, err := w.mapper.FetchAssetBatch(ctx, afterID, pageLimit)
		if err != nil {
			return result, fmt.Errorf("reindex: fetch batch (after %q): %w", afterID, err)
		}
		if len(batch) == 0 {
			break // no more assets
		}

		result.TotalAssets += len(batch)
		totalSeen += len(batch)

		// Map each asset to a point and accumulate.
		for _, asset := range batch {
			afterID = asset.ID // advance cursor

			point, err := w.mapper.AssetToPoint(ctx, asset, w.idxSchema)
			if err != nil {
				result.FailedAssets++
				result.FailedAssetIDs = append(result.FailedAssetIDs, asset.ID)
				continue
			}
			points = append(points, *point)
			batchCount++

			// Flush every 100 points.
			if len(points) >= 100 {
				if err := w.client.UpsertPoints(ctx, targetCollection, points); err != nil {
					return result, fmt.Errorf("reindex batch upsert: %w", err)
				}
				// Only count as indexed AFTER the batch commit
				// succeeds (Blocco 4c, July 2026).
				result.IndexedAssets += batchCount
				batchCount = 0
				points = points[:0]
			}
		}
	}

	// Flush remaining.
	if len(points) > 0 {
		if err := w.client.UpsertPoints(ctx, targetCollection, points); err != nil {
			return result, fmt.Errorf("reindex final batch upsert: %w", err)
		}
		result.IndexedAssets += batchCount
	}

	w.log.Info("reindex complete",
		zap.Int("total", result.TotalAssets),
		zap.Int("indexed", result.IndexedAssets),
		zap.Int("failed", result.FailedAssets),
		zap.String("collection", targetCollection))

	return result, nil
}

// ValidatePoint checks a point against the schema before upsert.
//
// QDRANT-001 closure (June 2026): error messages report the canonical
// asset ID (without the AssetIDPrefix namespace marker) so operators
// reading the dashboards see the same identifier they would on SQLite
// / Drive / Qdrant REST. Internal callers (PayloadMapper, IndexWriter)
// receive the prefixed form via point.ID and we strip it here at the
// public validation boundary.
func ValidatePoint(point *schema.Point, schema *schema.IndexSchema) error {
	if point == nil {
		return fmt.Errorf("point is nil")
	}
	if point.ID == "" {
		return fmt.Errorf("point ID must not be empty")
	}
	// AssetID is used purely to enrich error messages with a
	// human-readable identifier. Prefer `payload["asset_id"]` when
	// present (the canonical write path always populates it via
	// BuildPayload). Fall back to `point.ID` (the UUID v5 hash) when
	// the payload is missing or empty — this preserves backwards
	// compatibility with legacy code paths and unit-test fixtures
	// that construct bare Points without populating the payload.
	// QDRANT-001's silent-failure concern was about the IDENTITY
	// reverse-mapping from the UUID point, NOT about
	// the bare point.ID itself: the latter is a valid (if
	// operator-unfriendly) identifier, not a security bypass.
	var assetID string
	if point.Payload != nil {
		if id, ok := point.Payload["asset_id"].(string); ok && id != "" {
			assetID = id
		}
	}
	if assetID == "" {
		assetID = point.ID
	}

	vectors := point.Vectors
	if len(vectors) == 0 {
		return fmt.Errorf("point must have at least one vector")
	}

	for _, spec := range schema.DenseVectors {
		raw, ok := vectors[spec.Channel]
		if !ok {
			continue // optional channel
		}
		vec, ok := raw.([]float32)
		if !ok {
			return &transport.ErrVectorDimensionMismatch{
				Channel:  spec.Channel,
				Expected: spec.Dimensions,
				Actual:   0,
				AssetID:  assetID,
			}
		}
		if len(vec) == 0 {
			return &transport.ErrEmptyVector{Channel: spec.Channel, AssetID: assetID}
		}
		if len(vec) != spec.Dimensions {
			return &transport.ErrVectorDimensionMismatch{
				Channel:  spec.Channel,
				Expected: spec.Dimensions,
				Actual:   len(vec),
				AssetID:  assetID,
			}
		}
		for _, v := range vec {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return &transport.ErrNaNOrInf{Channel: spec.Channel, AssetID: assetID}
			}
		}
	}

	return nil
}

// ── Asset data types for the mapper ──────────────────────────────────

// AssetData is the canonical asset representation used by PayloadMapper.
// It mirrors the media_assets table columns needed for Qdrant points.
type AssetData struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Source    string `json:"source"`
	MediaType string `json:"media_type"`
	Status    string `json:"status"`
	// LifecycleState is the canonical search-filter payload key. Asset
	// store populates from media_assets.lifecycle_state when the column
	// exists; legacy rows fall back to Status-derived values so the
	// search adapter's filter key (lifecycle_state) is never empty.
	// See payload_mapper.canonicalLifecycleState for the prefer/fall-back
	// hierarchy used at write time.
	LifecycleState string   `json:"lifecycle_state,omitempty"`
	Language       string   `json:"language,omitempty"`
	Category       string   `json:"category,omitempty"`
	Style          string   `json:"style,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	SearchText     string   `json:"search_text,omitempty"`
	// DriveLink is the Drive web-view link for non-Qdrant legacy
	// callers. QDRANT-001 (June 2026): intentionally NOT emitted by
	// payload_mapper.BuildPayload — clients obtain a short-TTL
	// signed URL via delivery.Signer.BuildAuthorizedURL per asset.
	// Populated by asset_store.go from media_assets.drive_link for
	// ingest-path tracking / reconstruct-from-SQL flows; never
	// shipped to the vector index.
	DriveLink string `json:"drive_link,omitempty"`
	// LocalPath is the absolute filesystem path for non-Qdrant
	// legacy callers. QDRANT-001 (June 2026): intentionally NOT
	// emitted by payload_mapper.BuildPayload — the canonical search
	// index is locator-free. Populated by asset_store.go from
	// media_assets.local_path for ingest-time tracking only; never
	// shipped to the vector index. NOTE: future readers, please do
	// NOT remove this field on a cleanup pass; it is required by
	// `internal/application/{assets|clips}/ingest/*.go` flow
	// diagnostics, and removing it would silently break ingest
	// crash-trace logs.
	LocalPath      string                 `json:"local_path,omitempty"`
	YouTubeVideoID string                 `json:"youtube_video_id,omitempty"`
	YouTubeURL     string                 `json:"youtube_url,omitempty"`
	StartTime      string                 `json:"start_time,omitempty"`
	EndTime        string                 `json:"end_time,omitempty"`
	DurationMs     int64                  `json:"duration_ms,omitempty"`
	WorkspaceID    string                 `json:"workspace_id,omitempty"`
	ChannelID      string                 `json:"channel_id,omitempty"`
	License        string                 `json:"license,omitempty"`
	IndexVersion   string                 `json:"index_version,omitempty"`
	SourceVersion  string                 `json:"source_version,omitempty"`
	CreatedAt      string                 `json:"created_at,omitempty"`
	UpdatedAt      string                 `json:"updated_at,omitempty"`
	DeletedAt      string                 `json:"deleted_at,omitempty"`
	MetadataJSON   string                 `json:"-"`
	Metadata       map[string]interface{} `json:"-"`
	// Embeddings are populated by the mapper from DB columns.
	TextVector       []float32 `json:"-"`
	TranscriptVector []float32 `json:"-"`
	VisualVector     []float32 `json:"-"`
	AudioVector      []float32 `json:"-"`
	ContentHash      string    `json:"content_hash,omitempty"`
}

// AssetStore is the interface the PayloadMapper needs to fetch asset data.
type AssetStore interface {
	FetchAsset(ctx context.Context, assetID string) (*AssetData, error)
	ListAllAssetIDs(ctx context.Context) ([]string, error)
	// FetchAssetBatch returns a page of assets where id > afterID,
	// ordered by id ASC, limited to limit rows. Returns an empty
	// slice when no more rows exist. Used by ReindexAll for cursor-
	// based paginated scanning instead of loading all IDs into memory
	// and N+1 FetchAsset calls (HIGH #8, July 2026).
	FetchAssetBatch(ctx context.Context, afterID string, limit int) ([]*AssetData, error)
}
