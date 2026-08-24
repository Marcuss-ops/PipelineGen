// Package monitor — semantic analyzer port.
package assets

import (
	"context"
	"errors"

	transcript "github.com/Marcuss-ops/PipelineGen/internal/kernel/transcript"
)

// VideoAnalyzer scores relevance, classifies category, and extracts best segments.
type VideoAnalyzer interface {
	AnalyzeFull(ctx context.Context, doc transcript.Document, opts AnalyzeOptions) (Analysis, error)
}

// ErrLLMResponseInvalid is returned when the LLM response cannot be parsed into the expected JSON shape.
var ErrLLMResponseInvalid = errors.New("monitor: malformed LLM JSON response")

// ErrAnalyzeFullNotImplemented is returned by adapters that have not upgraded to AnalyzeFull.
var ErrAnalyzeFullNotImplemented = errors.New("monitor: AnalyzeFull not implemented on concrete adapter")

// AnalyzeOptions is the input shape for VideoAnalyzer.AnalyzeFull.
type AnalyzeOptions struct {
	SemanticKeywords []string
	CategoryFallback string
	MaxSegments      int
	SegmentPrompt    string
	MinScore         int
}
