// Package stockintelligence — local_search.go declares the candidate +
// request/result value types and the narrow ports the resolver consumes.
// Ports keep the package testable in isolation: concrete Qdrant/SQLite/
// Artlist adapters satisfy them at the composition root.
package stockintelligence

import (
	"context"
	"errors"
)

// Candidate is one local or provider asset candidate under consideration
// for a scene. It mirrors rust/mediasampler::Candidate so the Go
// resolver and the Rust sampler pass candidates across the boundary
// without translation.
type Candidate struct {
	// AssetID is the stable canonical asset id (used for cross-scene
	// reuse detection + SQLite hydration).
	AssetID string `json:"asset_id"`
	// Label is the human-facing label (entity / query / title) used by
	// the MediaSampler subject-match check.
	Label string `json:"label"`
	// GenericSimilarity is the raw local/Qdrant similarity in [0,1]. NOT
	// the final sampler score.
	GenericSimilarity float32 `json:"generic_similarity"`
	// OwnerSegmentID is the segment the candidate was discovered for; when
	// non-empty and different from the sampling scene, the sampler rejects
	// it with owner_mismatch.
	OwnerSegmentID string `json:"owner_segment_id,omitempty"`
	// Source is where the candidate came from ("local" or "provider").
	Source string `json:"source"`
}

// ResolveRequest is the canonical input to the resolver. It carries the
// SceneIR-derived subject + visual terms and the thresholds that decide
// when the provider live path is consulted.
type ResolveRequest struct {
	// SegmentID is the canonical segment the request is for.
	SegmentID string `json:"segment_id"`
	// Subject is the single canonical subject (matches SceneIR.Profile.Subject).
	Subject string `json:"subject"`
	// VisualTerms are the source-grounded anchors (matches
	// SceneIR.Profile.VisualTerms).
	VisualTerms []string `json:"visual_terms"`
	// Query is the provider-facing query string (built by the QueryPlanner).
	Query string `json:"query"`
	// LocalCandidateThreshold is the minimum local candidate count below
	// which the provider live path is consulted. Zero uses the resolver
	// default (10).
	LocalCandidateThreshold int `json:"local_candidate_threshold,omitempty"`
	// MinimumQuality is the minimum best-score below which the provider
	// live path is consulted. Zero uses the resolver default (0.6).
	MinimumQuality float32 `json:"minimum_quality,omitempty"`
}

// ResolveResult is the resolver output: the local candidates (and any
// provider fallback candidates), the winner asset id, and the count of
// provider live requests actually made (the metric MediaCert checks:
// 0 when local-first succeeded, 1 on fallback).
type ResolveResult struct {
	// SegmentID is the segment the result is for.
	SegmentID string `json:"segment_id"`
	// Candidates are the merged local + (any) provider candidates, ready
	// for the MediaSampler.
	Candidates []Candidate `json:"candidates"`
	// WinnerAssetID is the MediaSampler winner (empty when no candidate
	// reached minimum quality).
	WinnerAssetID string `json:"winner_asset_id,omitempty"`
	// ProviderLiveRequests is the number of provider live requests made
	// (0 when local-first succeeded, 1 on fallback, >1 only under
	// pathological repeated fallback).
	ProviderLiveRequests int `json:"provider_live_requests"`
	// LocalCandidateCount is the number of local candidates found before
	// any fallback.
	LocalCandidateCount int `json:"local_candidate_count"`
	// FallbackReason is non-empty when the provider live path was taken.
	FallbackReason string `json:"fallback_reason,omitempty"`
}

// LocalSearchPort is the Qdrant local search surface. The resolver consults
// it first (LOCAL FIRST). Concrete adapters (Qdrant Searcher-backed) are
// wired at the composition root.
type LocalSearchPort interface {
	// SearchLocal returns up to `limit` local candidates for the query,
	// each with its Qdrant similarity. Returns an empty slice (not an
	// error) when the local index has no matches.
	SearchLocal(ctx context.Context, query string, limit int) ([]Candidate, error)
}

// AssetHydratorPort is the SQLite truth surface. It hydrates the
// canonical asset metadata for the candidate AssetIDs the local search
// returned. SQLite is the truth; Qdrant is the search projection.
type AssetHydratorPort interface {
	// Hydrate returns the canonical asset labels (entity/title) for the
	// given asset ids. Missing ids return an empty label and are dropped
	// by the resolver.
	Hydrate(ctx context.Context, assetIDs []string) (map[string]string, error)
}

// ProviderClientPort is the live Artlist/stock browser surface. The
// resolver consults it ONLY when local-first did not satisfy the
// thresholds (PROVIDER SECOND).
type ProviderClientPort interface {
	// SearchProvider makes ONE live provider request for the query and
	// returns up to `limit` provider candidates. The resolver counts this
	// call so MediaCert can assert 0 requests on local-first success and
	// exactly 1 on fallback.
	SearchProvider(ctx context.Context, query string, limit int) ([]Candidate, error)
}

// ErrResolverNotWired is the fail-closed error when the resolver is
// constructed without the required ports.
var ErrResolverNotWired = errors.New("stockintelligence: resolver not wired (missing port)")
