// Package brain defines the canonical ports for the Brain capability.
// See types.go for the data shapes consumed and produced by these ports.
package brain

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
)

// Brain is the canonical single entry point of the brain capability.
// It receives a BrainRequest and returns a deterministic BrainResult
// containing the visual plan for every scene. All routes that need a
// visual plan (automatic generation, manual dashboard, batch, project
// regeneration) must pass through this port.
//
// Concrete implementations are wired once by the composition root and
// shared by every caller. The interface deliberately does not expose
// per-source or per-backend variants such as ResolveForYouTube or
// ResolveForImages; those are exactly the kind of parallel paths the
// brain architecture forbids.
type Brain interface {
	Resolve(ctx context.Context, req BrainRequest) (BrainResult, error)
}

// CandidateSearcher is the canonical port through which the brain
// federates searches across exact memory, local catalog, semantic
// backend and external providers. The brain does not know any
// concrete provider (Artlist, YouTube, Pexels, Qdrant, SQLite); it
// delegates every search to this single port.
//
// The concrete implementation is the canonical SearchFanOut, so a
// single aggregator owns fan-out, timeout, deduplication and ranking.
type CandidateSearcher interface {
	Search(ctx context.Context, query SearchQuery) (SearchResult, error)
}

// SearchQuery is the narrow input shape consumed by CandidateSearcher.
// It intentionally avoids provider-specific coordinates.
type SearchQuery struct {
	Text         string
	Language     string
	MediaTypes   []string
	Sources      []string
	Limit        int
	SearchPolicy media.ResolutionSearchPolicy
}

// SearchResult is the narrow output shape produced by CandidateSearcher.
type SearchResult struct {
	Candidates    []Candidate
	Partial       bool
	BackendErrors map[string]string
}

// Candidate is a single candidate returned by a search.
// It carries only the identifiers and metadata needed by the brain to
// rank and plan; it deliberately does not expose local paths, Drive
// links or provider internals.
type Candidate struct {
	ID                   string
	AssetID              string
	BindingID            string
	Provider             string
	SourceURL            string
	ThumbnailURL         string
	Title                string
	Description          string
	MediaType            string
	DurationMs           int64
	Score                float64
	MaterializationState string
	RightsStatus         string
	LicenseBasis         string
}
