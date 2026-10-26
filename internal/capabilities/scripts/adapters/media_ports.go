package adapters

import (
	"context"

	scriptmetrics "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports/metrics"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type InternetImageSearchRequest struct {
	SegmentID string
	Position  int
	TextHash  string
	Query     string
	Entity    string
	// QueryClass is the semantic routing class (`person`, `organization`,
	// `location`, `object`, `event`, `generic`). Empty keeps the canonical
	// provider cascade unchanged.
	QueryClass   string
	QueryOrigin  string
	TemplateID   string
	Illustrative bool
	Language     string
	Limit        int
	Provider     string
	// ForceRefresh bypasses durable retrieved-image reuse: the bytes behind
	// a stored URL can change upstream (source re-encode), so a recorded
	// hash is only a claim until bytes are re-downloaded and re-hashed
	// in-run. Callers set it from the plan's refresh flags.
	ForceRefresh bool
}

type InternetImageSearcher interface {
	SearchImages(ctx context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error)
}

type VidRushMetrics = scriptmetrics.VidRushMetrics

type MetadataGenerator interface {
	GenerateMetadata(ctx context.Context, req scriptpkg.MetadataGenerationRequest) ([]scriptpkg.VideoMetadata, error)
}
