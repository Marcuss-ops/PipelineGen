// Package entities provides entity extraction service layer.
package entities

import (
	"context"

	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// EntityService orchestrates entity extraction across the pipeline
type EntityService struct {
	extractor Extractor
	segmenter Segmenter
}

// NewEntityService creates a new entity service
func NewEntityService(extractor Extractor, segmenter Segmenter) *EntityService {
	return &EntityService{
		extractor: extractor,
		segmenter: segmenter,
	}
}

// AnalyzeScript performs complete entity analysis on a script:
// 1. Segments the script
// 2. Extracts entities from each segment
// 3. Returns structured analysis
func (s *EntityService) AnalyzeScript(ctx context.Context, script string, entityCount int, segmentConfig SegmentConfig) (*ScriptEntityAnalysis, error) {
	if script == "" {
		return nil, nil
	}

	// Step 1: Segment the script
	segments := s.segmenter.Split(script, segmentConfig)
	if len(segments) == 0 {
		return nil, nil
	}

	logger.Info("Script segmented for entity extraction",
		zap.Int("total_segments", len(segments)),
		zap.Int("entity_count_per_segment", entityCount),
	)

	// Step 2: Extract entities from all segments
	analysis, err := s.extractor.ExtractFromScript(segments, entityCount)
	if err != nil {
		logger.Warn("Entity extraction failed",
			zap.Error(err),
		)
		// Return empty analysis instead of error (non-critical)
		return &ScriptEntityAnalysis{
			TotalSegments:   len(segments),
			SegmentEntities: []SegmentEntityResult{},
			TotalEntities:   0,
		}, nil
	}

	logger.Info("Entity analysis completed",
		zap.Int("total_segments", analysis.TotalSegments),
		zap.Int("total_entities", analysis.TotalEntities),
	)

	return analysis, nil
}

// Segmenter returns the underlying segmenter for direct access
func (s *EntityService) Segmenter() Segmenter {
	return s.segmenter
}
