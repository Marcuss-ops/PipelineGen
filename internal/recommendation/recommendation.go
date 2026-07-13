// Package recommendation owns the asset-recommendation
// surface: given a query / asset, returns a list of recommended
// assets (similar clips, related content, "next best" picks).
//
// PR-YOUTUBE-SERVICE-SPLIT (July 2026): per godlike/06 SSOT
// (one canonical owner per fact), the recommendation contract
// is now its own package — surface-level, not a separate
// "engagement" package — separated from search/ranking code
// so future content-recommendation logic has a typed-narrow
// home.
//
// PR-YOUTUBE-SERVICE-SPLIT phase 1 (this commit): typed-narrow
// contract + StubAdapter that returns the canonical typed
// sentinel ErrRecommendationDisabled. godlike/07 fail-closed:
// never a silent empty-recommendations no-op (godlike/07
// NO-FAKE-AVAILABILITY).
//
// Phase 2 (next commit) will wire a real RecommendationBackend
// — likely a hybrid of the canonical search.Aggregator
// (similar-by-metadata) plus a vector-search sibling
// (similar-by-clipindexer). The phase-1 StubAdapter is the
// canonical godlike/06 SSOT placeholder for the
// not-yet-implemented mode: callers can errors.Is to detect
// it and route to "no recommendations" UI affordances.
package recommendation

import (
	"context"
	"errors"
	"fmt"
)

// Recommender is the canonical godlike/06 SSOT narrow port
// for the asset-recommendation surface.
type Recommender interface {
	// Recommend takes a query (asset ID + optional filters)
	// and returns the canonical list of recommended assets.
	// nil port → typed sentinel — never a silent empty list
	// (godlike/07). nil port path must be typed-branchable
	// via errors.Is against ErrRecommenderNotWired.
	Recommend(ctx context.Context, q *Query) ([]Recommended, error)
}

// Query is the typed-narrow input for the recommendation
// surface. Phase 2 will grow this with By{Vector,Metadata}
// filters; phase 1 keeps the surface minimal (godlike/06
// minimum cross-package contract).
type Query struct {
	AssetID    string
	SourceID   string
	Tags       []string
	Limit      int
	ByMetadata bool // phase-2 hint: metadata vs vector similarity
	ByVector   bool // phase-2 hint
}

// Recommended is the typed-narrow recommendation DTO.
type Recommended struct {
	AssetID   string
	SourceID  string
	Title     string
	Score     float64 // 0.0 - 1.0
	Reason    string  // human-readable: "similar metadata", "near vector", etc.
	Thumbnail string
}

// StubAdapter is the canonical godlike/07 fail-closed
// placeholder. Returns ErrRecommendationDisabled so callers
// can errors.Is to render the "no recommendations" UI
// affordance while phase 2 is being built.
//
// godlike/07 NO-FAKE-AVAILABILITY: this adapter MUST NOT
// silently return empty recommendations. The disabled-mode
// contract is observable to operators (the typed sentinel
// surfaces in logs / metrics / error envelopes).
type StubAdapter struct{}

// NewStubAdapter constructs the canonical not-yet-implemented
// Recommender. Always returns a valid *StubAdapter; failure
// to construct has no failure mode (the stub IS the canonical
// not-yet-implemented mode).
func NewStubAdapter() *StubAdapter {
	return &StubAdapter{}
}

// ErrRecommenderNotWired is the typed sentinel returned when
// the composition root fails to wire a real Recommender at
// build time (godlike/07 fail-closed).
var ErrRecommenderNotWired = fmt.Errorf("recommendation: recommender not wired (godlike/07 fail-closed)")

// ErrRecommendationDisabled is the typed sentinel returned by
// the StubAdapter when the recommendation surface is requested
// but the canonical implementation is not yet wired (godlike/07
// NO-FAKE-AVAILABILITY — never silent empty results).
var ErrRecommendationDisabled = errors.New("recommendation: surface not yet implemented (godlike/07 fail-closed; phase 2 promotes StubAdapter -> real backend)")

// Recommend returns the canonical ErrRecommendationDisabled
// sentinel. Callers errors.Is to render the "recommendations
// coming soon" UI affordance.
//
// Future phase-2 impl: switch on query.ByMetadata vs query.ByVector
// and dispatch to metadata-similarity (search.Aggregator) or
// vector-similarity (clipindexer.Similar) accordingly.
func (s *StubAdapter) Recommend(ctx context.Context, q *Query) ([]Recommended, error) {
	if s == nil {
		return nil, ErrRecommenderNotWired
	}
	if q == nil {
		return nil, fmt.Errorf("recommendation: query is nil (godlike/07 fail-closed)")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("%w (query.asset_id=%q)", ErrRecommendationDisabled, q.AssetID)
}

// Compile-time pinning: *StubAdapter satisfies Recommender.
var _ Recommender = (*StubAdapter)(nil)
