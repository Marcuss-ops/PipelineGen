// Package entities provides entity extraction and script analysis.
package entities

import (
	"context"
)

// Extractor interface for entity extraction implementations
type Extractor interface {
	ExtractFromSegment(text string, segmentIndex int, entityCount int) (*SegmentEntityResult, error)
	ExtractFromScript(segments []string, entityCount int) (*ScriptEntityAnalysis, error)
}

// Segmenter interface for script segmentation
type Segmenter interface {
	Split(text string, config SegmentConfig) []string
	CountWords(text string) int
	EstimateSegments(text string, wordsPerSegment int) int
}

// EntityAnalyzer interface for entity analysis orchestration
type EntityAnalyzer interface {
	AnalyzeScript(ctx context.Context, script string, entityCount int, segmentConfig SegmentConfig) (*ScriptEntityAnalysis, error)
	Segmenter() Segmenter
}
