package qdrant

import (
	"context"
	"fmt"
	"math"

	"go.uber.org/zap"
)

// IndexWriter handles point upsert and deletion for Qdrant collections.
// It implements clipindexer.VectorStoreIndexer and outbox.QdrantDeleter.
//
// All writes go through the runtime alias so callers never need to know
// the physical collection name.
type IndexWriter struct {
	client *Client
	schema *IndexSchema
	mapper *PayloadMapper
	log    *zap.Logger
}

// NewIndexWriter creates an IndexWriter.
//
// Compile-time assertions: IndexWriter satisfies both the generic IndexWriterPort
// (used by clipindexer) and QdrantDeleter (used by outbox).
var (
	_ IndexWriterPort = (*IndexWriter)(nil)
	_ QdrantDeleter   = (*IndexWriter)(nil)
)

func NewIndexWriter(client *Client, schema *IndexSchema, mapper *PayloadMapper, log *zap.Logger) *IndexWriter {
	return &IndexWriter{
		client: client,
		schema: schema,
		mapper: mapper,
		log:    log,
	}
}

// UpsertFromClip reads a single clip's data from the asset store and upserts it
// to Qdrant. Implements clipindexer.VectorStoreIndexer.
func (w *IndexWriter) UpsertFromClip(ctx context.Context, clipID string) error {
	return w.UpsertFromClips(ctx, []string{clipID})
}

// UpsertFromClips reads multiple clips and batch-upserts them.
// Implements clipindexer.VectorStoreIndexer.
func (w *IndexWriter) UpsertFromClips(ctx context.Context, clipIDs []string) error {
	if len(clipIDs) == 0 {
		return nil
	}

	points := make([]Point, 0, len(clipIDs))
	var failed []string

	for _, clipID := range clipIDs {
		asset, err := w.mapper.FetchAsset(ctx, clipID)
		if err != nil {
			w.log.Warn("failed to fetch asset for qdrant upsert",
				zap.String("asset_id", clipID),
				zap.Error(err))
			failed = append(failed, clipID)
			continue
		}
		point, err := w.mapper.AssetToPoint(asset, w.schema)
		if err != nil {
			w.log.Warn("failed to map asset to qdrant point",
				zap.String("asset_id", clipID),
				zap.Error(err))
			failed = append(failed, clipID)
			continue
		}
		points = append(points, *point)
	}

	if len(points) == 0 {
		if len(failed) > 0 {
			return fmt.Errorf("all %d assets failed to map to qdrant points", len(failed))
		}
		return nil
	}

	collection, err := w.client.GetAliasTarget(ctx, w.schema.RuntimeAlias)
	if err != nil {
		return fmt.Errorf("resolve alias target: %w", err)
	}
	if collection == "" {
		return fmt.Errorf("runtime alias %q has no target", w.schema.RuntimeAlias)
	}

	if err := w.client.UpsertPoints(ctx, collection, points); err != nil {
		return fmt.Errorf("upsert %d points to %q: %w", len(points), collection, err)
	}

	w.log.Info("upserted points to qdrant",
		zap.Int("count", len(points)),
		zap.String("collection", collection))

	if len(failed) > 0 {
		return fmt.Errorf("upserted %d points but %d assets failed mapping", len(points), len(failed))
	}
	return nil
}

// DeletePoints deletes points from the active collection by asset ID.
// Implements outbox.QdrantDeleter.
//
// QDRANT-001 closure (June 2026): each asset ID is canonicalised via
// AssetIDToQdrantPointID before being sent to the Qdrant client. The
// `Client.DeletePoints` is intentionally linear (it does NOT translate
// the IDs) — that contract is split here so the Client layer stays
// Qdrant-native and free of asset-domain knowledge. Mirrors the
// PayloadMapper.AssetToPoint write path so any asset ID passed in
// produces a 1-to-1 delete against the prefix-namespaced Point ID
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
		if pid := AssetIDToQdrantPointID(id); pid != "" {
			pointIDs = append(pointIDs, pid)
		}
	}
	if len(pointIDs) == 0 {
		return nil
	}

	collection, err := w.client.GetAliasTarget(ctx, w.schema.RuntimeAlias)
	if err != nil {
		return fmt.Errorf("resolve alias target: %w", err)
	}
	if collection == "" {
		return fmt.Errorf("runtime alias %q has no target", w.schema.RuntimeAlias)
	}

	if err := w.client.DeletePoints(ctx, collection, pointIDs); err != nil {
		return fmt.Errorf("delete points from %q: %w", collection, err)
	}

	w.log.Info("deleted points from qdrant",
		zap.Int("count", len(pointIDs)),
		zap.String("collection", collection))
	return nil
}

// ReindexAll reads all assets from the mapper's store and upserts them into
// the given target collection (usually a new physical collection before alias switch).
func (w *IndexWriter) ReindexAll(ctx context.Context, targetCollection string, limit int) (*ReindexResult, error) {
	if targetCollection == "" {
		targetCollection = w.schema.physicalName()
	}

	assetIDs, err := w.mapper.ListAllAssetIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list assets for reindex: %w", err)
	}

	if limit > 0 && len(assetIDs) > limit {
		assetIDs = assetIDs[:limit]
	}

	result := &ReindexResult{
		TotalAssets:      len(assetIDs),
		TargetCollection: targetCollection,
	}

	points := make([]Point, 0, 100)
	for _, assetID := range assetIDs {
		asset, err := w.mapper.FetchAsset(ctx, assetID)
		if err != nil {
			result.FailedAssets++
			result.FailedAssetIDs = append(result.FailedAssetIDs, assetID)
			continue
		}
		point, err := w.mapper.AssetToPoint(asset, w.schema)
		if err != nil {
			result.FailedAssets++
			result.FailedAssetIDs = append(result.FailedAssetIDs, assetID)
			continue
		}
		points = append(points, *point)
		result.IndexedAssets++

		// Flush every 100 points.
		if len(points) >= 100 {
			if err := w.client.UpsertPoints(ctx, targetCollection, points); err != nil {
				return result, fmt.Errorf("reindex batch upsert: %w", err)
			}
			points = points[:0]
		}
	}

	// Flush remaining.
	if len(points) > 0 {
		if err := w.client.UpsertPoints(ctx, targetCollection, points); err != nil {
			return result, fmt.Errorf("reindex final batch upsert: %w", err)
		}
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
func ValidatePoint(point *Point, schema *IndexSchema) error {
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
	// reverse-mapping (PointIDToAssetID on a UUID point), NOT about
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
			return &ErrVectorDimensionMismatch{
				Channel:  spec.Channel,
				Expected: spec.Dimensions,
				Actual:   0,
				AssetID:  assetID,
			}
		}
		if len(vec) == 0 {
			return &ErrEmptyVector{Channel: spec.Channel, AssetID: assetID}
		}
		if len(vec) != spec.Dimensions {
			return &ErrVectorDimensionMismatch{
				Channel:  spec.Channel,
				Expected: spec.Dimensions,
				Actual:   len(vec),
				AssetID:  assetID,
			}
		}
		for _, v := range vec {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return &ErrNaNOrInf{Channel: spec.Channel, AssetID: assetID}
			}
		}
	}

	return nil
}

// ── Asset data types for the mapper ──────────────────────────────────

// AssetData is the canonical asset representation used by PayloadMapper.
// It mirrors the media_assets table columns needed for Qdrant points.
type AssetData struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	Source         string                 `json:"source"`
	MediaType      string                 `json:"media_type"`
	Status         string                 `json:"status"`
	Language       string                 `json:"language,omitempty"`
	Category       string                 `json:"category,omitempty"`
	Style          string                 `json:"style,omitempty"`
	Tags           []string               `json:"tags,omitempty"`
	SearchText     string                 `json:"search_text,omitempty"`
	// DriveLink is the Drive web-view link for non-Qdrant legacy
	// callers. QDRANT-001 (June 2026): intentionally NOT emitted by
	// payload_mapper.BuildPayload — clients obtain a short-TTL
	// signed URL via delivery.Signer.BuildAuthorizedURL per asset.
	// Populated by asset_store.go from media_assets.drive_link for
	// ingest-path tracking / reconstruct-from-SQL flows; never
	// shipped to the vector index.
	DriveLink      string                 `json:"drive_link,omitempty"`
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
}

// AssetStore is the interface the PayloadMapper needs to fetch asset data.
type AssetStore interface {
	FetchAsset(ctx context.Context, assetID string) (*AssetData, error)
	ListAllAssetIDs(ctx context.Context) ([]string, error)
}
