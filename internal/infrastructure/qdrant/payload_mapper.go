package qdrant

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"go.uber.org/zap"
)

// PayloadMapper converts internal AssetData to Qdrant Point representations.
// It is the SINGLE place where vector names, payload fields, and embedding
// channel mapping are configured — no hardcoded names anywhere else.
type PayloadMapper struct {
	store AssetStore
	log   *zap.Logger
}

// NewPayloadMapper creates a PayloadMapper.
func NewPayloadMapper(store AssetStore, log *zap.Logger) *PayloadMapper {
	return &PayloadMapper{
		store: store,
		log:   log,
	}
}

// FetchAsset delegates to the AssetStore.
func (m *PayloadMapper) FetchAsset(ctx context.Context, assetID string) (*AssetData, error) {
	return m.store.FetchAsset(ctx, assetID)
}

// ListAllAssetIDs delegates to the AssetStore.
func (m *PayloadMapper) ListAllAssetIDs(ctx context.Context) ([]string, error) {
	return m.store.ListAllAssetIDs(ctx)
}

// AssetToPoint converts an AssetData to a Qdrant Point using the manifest.
//
// Rules (per QDRANT-003):
//   - Dense vector names come from the manifest, not hardcoded constants.
//   - Audio is included ONLY when the vector is present and the channel is active.
//   - Each vector's dimension is validated before the HTTP call.
//   - NaN/Inf values are rejected.
//   - Empty vectors for required channels are rejected.
//   - Assets with invalid vectors are NOT silently skipped — errors are typed.
//
// QDRANT-001 (June 2026): the point ID is canonicalised via
// AssetIDToQdrantPointID so all writes share a single translation rule
// (UUID v5 SHA-1 with project-namespacing). UUID v5 is one-way; the
// canonical asset_id is read directly from point.Payload["asset_id"]
// when a point is retrieved (see verifier.go::VerifyReindex).
// raw asset.ID literals in the qdrant package are an anti-pattern;
// this is the only legal site that derives a Qdrant point ID from a
// media_assets.id.
func (m *PayloadMapper) AssetToPoint(asset *AssetData, schema *IndexSchema) (*Point, error) {
	if asset == nil {
		return nil, fmt.Errorf("asset is nil")
	}
	if asset.ID == "" {
		return nil, fmt.Errorf("asset ID must not be empty")
	}

	vectors := make(map[string]interface{})
	payload := BuildPayload(asset, schema)

	// Map each dense vector channel.
	for _, spec := range schema.DenseVectors {
		vec := m.getVectorForChannel(asset, spec.Channel)
		if vec == nil {
			// Audio is optional per spec.
			if spec.Channel == "audio" {
				continue
			}
			// Text is required for indexed assets.
			// Transcript falls back to text vector when absent.
			if spec.Channel == "text" {
				return nil, &ErrEmptyVector{
					Channel: spec.Channel,
					AssetID: asset.ID,
				}
			}
			if spec.Channel == "transcript" {
				// Fall back to text vector when transcript is unavailable.
				// Mirrors sync_drive_qdrant.py behaviour.
				if asset.TextVector != nil {
					vectors[spec.Channel] = asset.TextVector
					continue
				}
				return nil, &ErrEmptyVector{
					Channel: spec.Channel,
					AssetID: asset.ID,
				}
			}
			continue
		}

		if len(vec) != spec.Dimensions {
			return nil, &ErrVectorDimensionMismatch{
				Channel:  spec.Channel,
				Expected: spec.Dimensions,
				Actual:   len(vec),
				AssetID:  asset.ID,
			}
		}
		for _, v := range vec {
			if isNaNOrInf(v) {
				return nil, &ErrNaNOrInf{
					Channel: spec.Channel,
					AssetID: asset.ID,
				}
			}
		}
		vectors[spec.Channel] = vec
	}

	// Map sparse vectors.
	// QDRANT-003: the collection is configured with server-side BM25
	// (modifier: "bm25" on createPhysicalCollection). Qdrant generates
	// sparse vectors from the search_text payload field automatically.
	// No client-side sparse vector computation is needed at upsert time.
	// Query-time sparse vectors use pkg/bm25.Tokenize.
	for _, spec := range schema.SparseVectors {
		switch spec.Channel {
		case "bm25_text":
			// Server-side BM25 — Qdrant reads search_text from payload.
			// Ensure the payload includes search_text (BuildPayload above).
			if asset.SearchText == "" {
				m.log.Debug("bm25_text: no search_text for asset, BM25 will be empty",
					zap.String("asset_id", asset.ID))
			}
		}
	}

	return &Point{
		ID:      AssetIDToQdrantPointID(asset.ID),
		Vectors: vectors,
		Payload: payload,
	}, nil
}

// getVectorForChannel returns the embedding vector for a given channel.
func (m *PayloadMapper) getVectorForChannel(asset *AssetData, channel string) []float32 {
	switch channel {
	case "text":
		return asset.TextVector
	case "transcript":
		return asset.TranscriptVector
	case "visual":
		return asset.VisualVector
	case "audio":
		return asset.AudioVector
	default:
		return nil
	}
}

// BuildPayload constructs the Qdrant payload from an AssetData and schema.
// It includes only data necessary for filtering, ranking, and hydration.
// No tokens, secrets, or physical paths are stored directly.
//
// QDRANT-004 PR2 (June 2026): the canonical lifecycle key is
// `lifecycle_state` (SSOT — matches media_assets.lifecycle_state in
// SQLite, qdrant.DefaultV3Schema().PayloadIndexes, and the search
// adapter filter). The previous `status` key was a legacy alias from
// pre-QDRANT-004 ingest pipelines; it has been retired from the
// writer (here), the reader (search_adapter, clip_search_adapter),
// and the manifest (DefaultV3Schema.PayloadIndexes). One-shot
// migration of historical points is the QDRANT-005B reconciler's
// repair path (target wave); until then legacy points carry both
// keys and the reader falls through silently.
func BuildPayload(asset *AssetData, schema *IndexSchema) map[string]interface{} {
	payload := map[string]interface{}{
		"asset_id":        asset.ID,
		"lifecycle_state": asset.LifecycleState,
		"source":          asset.Source,
		"media_type":      asset.MediaType,
	}

	if asset.WorkspaceID != "" {
		payload["workspace_id"] = asset.WorkspaceID
	}
	if asset.Language != "" {
		payload["language"] = asset.Language
	}
	if asset.Category != "" {
		payload["category"] = asset.Category
	}
	if asset.Style != "" {
		payload["style"] = asset.Style
	}
	if asset.ChannelID != "" {
		payload["channel_id"] = asset.ChannelID
	}
	if asset.License != "" {
		payload["license"] = asset.License
	}
	if asset.DurationMs > 0 {
		payload["duration_ms"] = asset.DurationMs
	}
	if asset.IndexVersion != "" {
		payload["index_version"] = asset.IndexVersion
	}
	if asset.SourceVersion != "" {
		payload["source_version"] = asset.SourceVersion
	}
	if asset.CreatedAt != "" {
		payload["created_at"] = asset.CreatedAt
	}
	if asset.UpdatedAt != "" {
		payload["updated_at"] = asset.UpdatedAt
	}
	if asset.DeletedAt != "" {
		payload["deleted_at"] = asset.DeletedAt
	}

	// Embedding version annotations (per channel).
	for _, spec := range schema.DenseVectors {
		key := fmt.Sprintf("embedding_version_%s", spec.Channel)
		if spec.ModelVersion != "" {
			payload[key] = spec.ModelVersion
		}
	}

	// Optional enrichment fields (for hydration, not search).
	if asset.Name != "" {
		payload["name"] = asset.Name
	}
	// QDRANT-001 (June 2026) closure: drive_link is intentionally
	// NOT written to the Qdrant payload. Drive web-view links are
	// server-internal locators that the canonical search index
	// must not leak. Clients that need a signed URL for an asset
	// go through `delivery.Signer.BuildAuthorizedURL(ctx, ws,
	// assetID)` — see mediasearch.Service for the wiring. The
	// LocalPath / AssetData field is still populated by
	// asset_store.go for *non-payload* uses (ingest-time path
	// tracking) but is never shipped to Qdrant.
	//
	// Stale payload cleanup: legacy upserts from before this
	// commit still contain a `drive_link` key on existing points.
	// search_adapter.go no longer reads it (the field is gone from
	// SearchResult), so the leakage path is closed at the
	// application boundary even for old points. A background
	// reconcile phase (QDRANT-005 territory) can prune the payload
	// keys server-side via `payload_key_drop` once the gate
	// stabilises.
	if len(asset.Tags) > 0 {
		payload["tags"] = asset.Tags
	}
	if asset.SearchText != "" {
		payload["search_text"] = asset.SearchText
	}
	if asset.YouTubeVideoID != "" {
		payload["youtube_video_id"] = asset.YouTubeVideoID
	}
	if asset.YouTubeURL != "" {
		payload["youtube_url"] = asset.YouTubeURL
	}

	// Asset metadata (non-sensitive fields only).
	parseMetadataJSON(asset)
	if asset.Metadata != nil {
		if title, ok := asset.Metadata["title"].(string); ok && title != "" {
			payload["title"] = title
		}
		if desc, ok := asset.Metadata["description"].(string); ok && desc != "" {
			payload["description"] = desc
		}
	}

	return payload
}

// parseMetadataJSON lazily parses metadata JSON on first access.
func parseMetadataJSON(asset *AssetData) {
	if asset.Metadata != nil || asset.MetadataJSON == "" {
		return
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(asset.MetadataJSON), &m); err == nil {
		asset.Metadata = m
	}
}

func isNaNOrInf(v float32) bool {
	return math.IsNaN(float64(v)) || math.IsInf(float64(v), 0)
}
