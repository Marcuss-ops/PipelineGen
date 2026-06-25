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
func (w *IndexWriter) DeletePoints(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	collection, err := w.client.GetAliasTarget(ctx, w.schema.RuntimeAlias)
	if err != nil {
		return fmt.Errorf("resolve alias target: %w", err)
	}
	if collection == "" {
		return fmt.Errorf("runtime alias %q has no target", w.schema.RuntimeAlias)
	}

	if err := w.client.DeletePoints(ctx, collection, ids); err != nil {
		return fmt.Errorf("delete points from %q: %w", collection, err)
	}

	w.log.Info("deleted points from qdrant",
		zap.Int("count", len(ids)),
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
func ValidatePoint(point *Point, schema *IndexSchema) error {
	if point == nil {
		return fmt.Errorf("point is nil")
	}
	if point.ID == "" {
		return fmt.Errorf("point ID must not be empty")
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
				AssetID:  point.ID,
			}
		}
		if len(vec) == 0 {
			return &ErrEmptyVector{Channel: spec.Channel, AssetID: point.ID}
		}
		if len(vec) != spec.Dimensions {
			return &ErrVectorDimensionMismatch{
				Channel:  spec.Channel,
				Expected: spec.Dimensions,
				Actual:   len(vec),
				AssetID:  point.ID,
			}
		}
		for _, v := range vec {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return &ErrNaNOrInf{Channel: spec.Channel, AssetID: point.ID}
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
	DriveLink      string                 `json:"drive_link,omitempty"`
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
