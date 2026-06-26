package qdrant

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"go.uber.org/zap"
)

// BM25Params holds the configurable BM25 scoring parameters.
// k1 controls term frequency saturation (default 1.2).
// b controls document length normalization (default 0.75).
// AvgDocLength is the average document length in tokens across the corpus.
// IDFTable maps tokens → inverse document frequency; nil means IDF=1.0 fallback.
type BM25Params struct {
	K1           float64            `json:"k1"`
	B            float64            `json:"b"`
	AvgDocLength float64            `json:"avg_doc_length"`
	IDFTable     map[string]float64 `json:"-"`
}

// DefaultBM25Params returns standard BM25 parameters.
func DefaultBM25Params() *BM25Params {
	return &BM25Params{
		K1:           1.2,
		B:            0.75,
		AvgDocLength: 50.0, // reasonable default for short media descriptions
	}
}

// PayloadMapper converts internal AssetData to Qdrant Point representations.
// It is the SINGLE place where vector names, payload fields, and embedding
// channel mapping are configured — no hardcoded names anywhere else.
type PayloadMapper struct {
	store      AssetStore
	bm25Params *BM25Params
	log        *zap.Logger
}

// NewPayloadMapper creates a PayloadMapper.
func NewPayloadMapper(store AssetStore, log *zap.Logger) *PayloadMapper {
	return &PayloadMapper{
		store:      store,
		bm25Params: DefaultBM25Params(),
		log:        log,
	}
}

// SetBM25Params overrides the default BM25 parameters. Call before reindex.
func (m *PayloadMapper) SetBM25Params(p *BM25Params) {
	if p != nil {
		m.bm25Params = p
	}
}

// BuildIDFTable computes IDF values from a corpus of tokenized documents.
// n_t = number of documents containing term t; N = total documents.
// IDF(t) = log((N - n_t + 0.5) / (n_t + 0.5) + 1)
func BuildIDFTable(docs [][]string) map[string]float64 {
	if len(docs) == 0 {
		return nil
	}
	N := float64(len(docs))
	df := make(map[string]int) // document frequency
	for _, doc := range docs {
		seen := make(map[string]bool)
		for _, t := range doc {
			if !seen[t] {
				df[t]++
				seen[t] = true
			}
		}
	}
	idf := make(map[string]float64, len(df))
	for t, nt := range df {
		ntf := float64(nt)
		idf[t] = math.Log((N-ntf+0.5)/(ntf+0.5) + 1.0)
	}
	return idf
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
		ID:      asset.ID,
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
func BuildPayload(asset *AssetData, schema *IndexSchema) map[string]interface{} {
	payload := map[string]interface{}{
		"asset_id":   asset.ID,
		"status":     asset.Status,
		"source":     asset.Source,
		"media_type": asset.MediaType,
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

func tokenize(text string) []string {
	var tokens []string
	word := make([]byte, 0, 32)
	for i := 0; i < len(text); i++ {
		c := text[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			word = append(word, c|0x20) // lowercase
		} else if len(word) > 0 {
			tokens = append(tokens, string(word))
			word = word[:0]
		}
	}
	if len(word) > 0 {
		tokens = append(tokens, string(word))
	}
	return tokens
}
