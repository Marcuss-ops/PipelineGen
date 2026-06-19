package vectorstore

import (
	"context"
	"fmt"
	"strconv"

	timeutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"
)

// UpsertAsset indexes a single asset as a point with named vectors.
func (c *QdrantClient) UpsertAsset(ctx context.Context, asset VectorAsset) error {
	point, err := c.buildPoint(asset)
	if err != nil {
		return err
	}

	upsertReq := map[string]any{
		"points": []any{point},
	}

	_, err = c.qdrantRequest(ctx, "PUT",
		fmt.Sprintf("/collections/%s/points?wait=true", c.operationCollection()), upsertReq)
	if err != nil {
		return fmt.Errorf("upsert point: %w", err)
	}

	return nil
}

// UpsertAssets indexes multiple assets in a single batch operation.
// Uses Qdrant's batch points API for up to 100x throughput vs sequential upserts.
func (c *QdrantClient) UpsertAssets(ctx context.Context, assets []VectorAsset) error {
	if len(assets) == 0 {
		return nil
	}

	points := make([]any, 0, len(assets))
	for _, asset := range assets {
		point, err := c.buildPoint(asset)
		if err != nil {
			return fmt.Errorf("build point %s: %w", asset.AssetID, err)
		}
		points = append(points, point)
	}

	upsertReq := map[string]any{
		"points": points,
	}

	_, err := c.qdrantRequest(ctx, "PUT",
		fmt.Sprintf("/collections/%s/points?wait=true", c.operationCollection()), upsertReq)
	if err != nil {
		return fmt.Errorf("upsert batch of %d points: %w", len(assets), err)
	}

	return nil
}

// buildPoint constructs a Qdrant point (id + vectors + payload) from a VectorAsset.
// Qdrant requires point IDs to be either an unsigned integer or a UUID string.
// String IDs like "760198" are rejected, so we convert them.
func (c *QdrantClient) buildPoint(asset VectorAsset) (map[string]any, error) {
	vectors := make(map[string][]float32)
	if len(asset.TextEmbedding) > 0 {
		vectors[c.cfg.TextVectorName] = asset.TextEmbedding
	}
	if len(asset.VisualEmbedding) > 0 {
		vectors[c.cfg.VisualVectorName] = asset.VisualEmbedding
	}
	if len(asset.AudioEmbedding) > 0 && c.cfg.AudioVectorName != "" {
		vectors[c.cfg.AudioVectorName] = asset.AudioEmbedding
	}
	if len(asset.TranscriptEmbedding) > 0 && c.cfg.TranscriptVectorName != "" {
		vectors[c.cfg.TranscriptVectorName] = asset.TranscriptEmbedding
	}

	if len(vectors) == 0 && asset.SparseBM25 == nil {
		return nil, fmt.Errorf("no embeddings provided for asset %s", asset.AssetID)
	}

	pointVectors := make(map[string]any)
	for name, vec := range vectors {
		pointVectors[name] = vec
	}

	if asset.SparseBM25 != nil && c.cfg.SparseVectorName != "" {
		pointVectors[c.cfg.SparseVectorName] = map[string]any{
			"indices": asset.SparseBM25.Indices,
			"values":  asset.SparseBM25.Values,
		}
	}

	payload := map[string]any{
		"asset_id":            asset.AssetID,
		"source":              asset.Source,
		"name":                asset.Name,
		"local_path":          asset.LocalPath,
		"drive_link":          asset.DriveLink,
		"category":            asset.Category,
		"style":               asset.Style,
		"media_type":          asset.MediaType,
		"duration_ms":         asset.DurationMs,
		"tags":                asset.Tags,
		"search_text":         asset.SearchText,
		"language":            asset.Language,
		"youtube_video_id":    asset.YouTubeVideoID,
		"youtube_url":         asset.YouTubeURL,
		"start_time":          asset.StartTime,
		"end_time":            asset.EndTime,
		"visual_embedding":    asset.VisualEmbedding,
		"embedding_version":   asset.EmbeddingVersion,
		"search_text_version": asset.SearchTextVersion,
	}

	if !asset.CreatedAt.IsZero() {
		payload["created_at"] = timeutil.FormatRFC3339(asset.CreatedAt)
	}

	// Convert asset ID to Qdrant-compatible format:
	// - If the ID is a numeric string, parse it as uint64 (number, not string)
	// - Otherwise, generate a deterministic UUID v3 from the string
	pointID := qdrantPointID(asset.AssetID)

	return map[string]any{
		"id":      pointID,
		"vector":  pointVectors,
		"payload": payload,
	}, nil
}

// qdrantPointID converts a string asset ID to a format Qdrant accepts.
// Qdrant point IDs must be uint64 or UUID strings.
func qdrantPointID(id string) any {
	if n, err := strconv.ParseUint(id, 10, 64); err == nil {
		return n
	}
	return fnv64(id)
}

// fnv64 computes a FNV-1a 64-bit hash from a string.
// This is deterministic and produces a uint64 suitable as a Qdrant point ID.
func fnv64(s string) uint64 {
	h := uint64(14695981039346656037)
	const prime64 = uint64(1099511628211)
	for _, c := range s {
		h ^= uint64(c)
		h *= prime64
	}
	return h
}
