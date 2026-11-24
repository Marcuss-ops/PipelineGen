package contract

import (
	"context"

	scriptmetrics "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports/metrics"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// InternetImageSearchRequest is the provider-neutral image search request.
type InternetImageSearchRequest struct {
	SegmentID    string
	Position     int
	TextHash     string
	Query        string
	Entity       string
	QueryClass   string
	QueryOrigin  string
	TemplateID   string
	Illustrative bool
	Language     string
	Limit        int
	Provider     string
	ForceRefresh bool
}

// InternetImageSearcher searches provider images for one request.
type InternetImageSearcher interface {
	SearchImages(ctx context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error)
}

// VidRushMetrics is the shared provider metrics surface.
type VidRushMetrics = scriptmetrics.VidRushMetrics

// VidRushTimingMetrics is the optional duration-observation surface shared by
// the VidRush processors and the canonical timing adapter.
type VidRushTimingMetrics interface {
	ObserveProcessorDuration(processor string, seconds float64)
	ObserveProviderDuration(provider string, seconds float64)
}
