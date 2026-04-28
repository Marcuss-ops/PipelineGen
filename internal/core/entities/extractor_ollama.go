// Package entities provides entity extraction using Ollama API.
package entities

import (
	"context"

	"velox/go-master/internal/ml/ollama/client"
	"velox/go-master/internal/ml/ollama/types"
)

// OllamaExtractor implements entities.Extractor using Ollama
type OllamaExtractor struct {
	client *client.Client
}

// NewOllamaExtractor creates a new Ollama-based entity extractor
func NewOllamaExtractor(c *client.Client) *OllamaExtractor {
	return &OllamaExtractor{
		client: c,
	}
}

// ExtractFromSegment extracts entities from a single text segment
func (e *OllamaExtractor) ExtractFromSegment(text string, segmentIndex int, entityCount int) (*SegmentEntityResult, error) {
	req := types.EntityExtractionRequest{
		SegmentText:  text,
		SegmentIndex: segmentIndex,
		EntityCount:  entityCount,
	}

	result, err := e.client.ExtractEntitiesFromSegment(context.Background(), req)
	if err != nil {
		return nil, err
	}

	return &SegmentEntityResult{
		SegmentIndex:     result.SegmentIndex,
		SegmentText:      text,
		FrasiImportanti:  result.FrasiImportanti,
		EntitaSenzaTesto: result.EntitaSenzaTesto,
		NomiSpeciali:     result.NomiSpeciali,
		ParoleImportanti: result.ParoleImportanti,
	}, nil
}

// ExtractFromScript extracts entities from all segments of a script
func (e *OllamaExtractor) ExtractFromScript(segments []string, entityCount int) (*ScriptEntityAnalysis, error) {
	analysis, err := e.client.ExtractEntitiesFromScript(context.Background(), segments, entityCount)
	if err != nil {
		return nil, err
	}

	// Convert to domain types
	segmentEntities := make([]SegmentEntityResult, len(analysis.SegmentEntities))
	for i, seg := range analysis.SegmentEntities {
		segmentEntities[i] = SegmentEntityResult{
			SegmentIndex:     seg.SegmentIndex,
			SegmentText:      seg.SegmentText,
			FrasiImportanti:  seg.FrasiImportanti,
			EntitaSenzaTesto: seg.EntitaSenzaTesto,
			NomiSpeciali:     seg.NomiSpeciali,
			ParoleImportanti: seg.ParoleImportanti,
		}
	}

	return &ScriptEntityAnalysis{
		TotalSegments:         analysis.TotalSegments,
		SegmentEntities:       segmentEntities,
		TotalEntities:         analysis.TotalEntities,
		EntityCountPerSegment: analysis.EntityCountPerSegment,
	}, nil
}
