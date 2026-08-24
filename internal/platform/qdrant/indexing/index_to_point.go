// Package indexing — index_to_point.go: IndexDocument → Qdrant schema.Point wire shaping.
//
// Extracted from payload_mapper_document.go (July 2026).
// Owns: IndexDocumentToPoint.
package indexing

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// IndexDocumentToPoint is the canonical wire-shaping layer (PR 6).
// Reads an IndexDocument (the airlock output) and emits the Qdrant
// schema.Point shape. Mirrors the validation + sparse-channel handling of
// AssetToPoint on the IndexDocument side. Callers that already have
// an IndexDocument skip the AssetToIndexDocument step.
//
// Sparse-channel wire shape: Qdrant requires `{text, model}` for
// server-side BM25 inference; the bm25_text artifact records the
// model name; SearchText comes from the document. Empty SearchText
// drops the channel (mirrors the existing AssetToPoint behavior for
// backward compatibility with the PR 2 BM25 contract).
func (m *PayloadMapper) IndexDocumentToPoint(doc *IndexDocument, idxSchema *schema.IndexSchema) (*schema.Point, error) {
	if doc == nil {
		return nil, fmt.Errorf("index document is nil")
	}
	if doc.AssetID == "" {
		return nil, fmt.Errorf("index document AssetID must not be empty")
	}
	vectors := make(map[string]any)
	for channel, artifact := range doc.Embeddings {
		switch channel {
		case ChannelText, ChannelTranscript, ChannelVisual, ChannelAudio:
			if artifact.Values == nil {
				continue
			}
			vectors[string(channel)] = artifact.Values
		case ChannelBM25Text:
			if doc.SearchText == "" {
				if m.log != nil {
					m.log.Debug("sparse channel: no search_text in doc, channel dropped",
						zap.String("asset_id", doc.AssetID),
						zap.String("channel", string(channel)))
				}
				continue
			}
			vectors[string(channel)] = map[string]any{
				"text":  doc.SearchText,
				"model": artifact.Model,
			}
		default:
			if artifact.Values != nil {
				vectors[string(channel)] = artifact.Values
			}
		}
	}
	return &schema.Point{
		ID:      schema.AssetIDToQdrantPointID(doc.AssetID),
		Vectors: vectors,
		Payload: BuildPayloadFromDocument(doc, idxSchema),
	}, nil
}
