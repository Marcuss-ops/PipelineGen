// Package adapters — search_fanout.go is the canonical composition-
// root adapter that bridges mediamemory's narrow SearchFanOut port
// to the canonical search.SearchFanOut surface.
//
// godlike/06 SSOT (capability composition): the mediamemory
// capability owns its own SearchFanOut envelope (SearchFanOutQuery /
// SearchFanOutResult) precisely because the canonical search.Result
// carries provider-internal coordinates the mediamemory capability
// must not import (per QDRANT-004 invariant: no LocalPath /
// DriveLink leaked into downstream consumers). This adapter is
// the SOLE bridge between the two surfaces — production wiring
// mounts it once at composition root, in the same vein as the
// sister adapters in internal/app/*.
//
// godlike/07 NO-FAKE-AVAILABILITY: errors from the inner
// search.SearchFanOut propagate with %w so the resolver can branch
// via errors.Is against the canonical search.Err* sentinels
// (ErrNoEligibleBackends, ErrAllBackendsFailed, ...).
// Partial / BackendErrors propagate verbatim — the resolver
// already handles Result.Partial in cascadeWarns.
package adapters

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
)

// SearchFanOutAdapter implements mediamemory.SearchFanOut on top
// of the canonical search.SearchFanOut (the one that wraps
// search.Aggregator + telemetry decorator per PR 9).
//
// godlike/06 SSOT: this is the SOLE bridge between the two
// surfaces. Composition root must wire ONE instance (singleton)
// and share it across all mediamemory consumers.
type SearchFanOutAdapter struct {
	inner search.SearchFanOut
}

// NewSearchFanOutAdapter constructs the adapter. inner MUST be
// non-nil; passing nil returns a typed error surface for callers
// that probe at composition time.
//
// godlike/06 SSOT (constructor invariant): the adapter does NOT
// substitute a noop when inner is nil — that would silently
// degrade to zero-candidate behaviour. Production composition root
// MUST inject a fully-wired inner (post-PR-9 telemetry decorator).
func NewSearchFanOutAdapter(inner search.SearchFanOut) (*SearchFanOutAdapter, error) {
	if inner == nil {
		return nil, mediamemory.ErrCandidateNotFound // typed sentinel for fail-closed composition
	}
	return &SearchFanOutAdapter{inner: inner}, nil
}

// Compile-time assertion: SearchFanOutAdapter implements
// mediamemory.SearchFanOut. Drift surfaces as a build error.
var _ mediamemory.SearchFanOut = (*SearchFanOutAdapter)(nil)

// Search translates the mediamemory envelope to the canonical
// search.Query, dispatches to inner, and projects the result back
// to the mediamemory envelope.
//
// godlike/07 NO-FAKE-AVAILABILITY: an inner-typed nil result is
// surfaced as wrapped mediamemory.ErrCandidateNotFound (never as a
// silent zero-candidate success). ProviderErrors from the inner
// search.Result are translated into BackendNames/BackendErrors so
// the resolver's cascadeWarns can branch on per-backend failures.
func (a *SearchFanOutAdapter) Search(ctx context.Context, q mediamemory.SearchFanOutQuery) (mediamemory.SearchFanOutResult, error) {
	if a == nil || a.inner == nil {
		return mediamemory.SearchFanOutResult{}, fmt.Errorf(
			"%w: search fanout adapter has nil inner (composition wiring failed)",
			mediamemory.ErrCandidateNotFound,
		)
	}

	policy := q.SearchPolicy
	mode := search.SearchMode(media.SearchModeToSearch(policy.Mode))
	// The policy resolver already applies the canonical default; the
	// adapter forwards the resolved mode verbatim and does not invent
	// a local default.
	if policy.Language == "" {
		policy.Language = q.Language
	}
	if len(policy.MediaTypes) == 0 {
		policy.MediaTypes = append([]string(nil), q.MediaTypes...)
	}
	if len(policy.AllowedProviders) == 0 {
		policy.AllowedProviders = append([]string(nil), q.Sources...)
	}
	if policy.MaxCandidates <= 0 && q.Limit > 0 {
		policy.MaxCandidates = q.Limit
	}
	if policy.MaxCandidates <= 0 {
		policy.MaxCandidates = search.DefaultLimit
	}

	canonicalQuery := search.Query{
		Text:           q.Text,
		Limit:          clampLimit(policy.MaxCandidates),
		Mode:           mode,
		Universe:       universeFromAllowExternal(policy.AllowExternal),
		MediaTypes:     append([]string(nil), policy.MediaTypes...),
		Sources:        append([]string(nil), policy.AllowedProviders...),
		AllowExternal:  policy.AllowExternal,
		CacheRead:      policy.CacheRead,
		PreferApproved: policy.PreferApproved,
		Filters: search.Filters{
			Language: policy.Language,
		},
		// Phase 1.x: Actor is zero-value (no workspace scoping at
		// the SearchFanOut boundary; production wiring adds the
		// Actor from upstream context in Fase 2.x).
	}

	res, err := a.inner.Search(ctx, canonicalQuery)
	if err != nil {
		return mediamemory.SearchFanOutResult{}, fmt.Errorf(
			"mediamemory: search fanout adapter inner.Search: %w",
			err,
		)
	}
	if res == nil {
		return mediamemory.SearchFanOutResult{}, fmt.Errorf(
			"mediamemory: search fanout adapter returned nil result: %w",
			mediamemory.ErrCandidateNotFound,
		)
	}

	out := mediamemory.SearchFanOutResult{
		Partial:       res.Partial,
		BackendNames:  make([]string, 0, len(res.ProviderErrors)),
		BackendErrors: make(map[string]string, len(res.ProviderErrors)),
		Candidates:    make([]mediamemory.MediaCandidate, 0, len(res.Items)),
	}

	for name, msg := range res.ProviderErrors {
		out.BackendNames = append(out.BackendNames, name)
		out.BackendErrors[name] = msg
	}

	for _, c := range res.Items {
		out.Candidates = append(out.Candidates, mediamemory.MediaCandidate{
			// godlike/06 SSOT (QDRANT-004 invariant): no
			// LocalPath / DriveLink leaks across this boundary.
			// The mediamemory MediaCandidate carries AssetID +
			// source URL for the linker/discovery worker only.
			Provider:              c.Source,
			ProviderAssetID:       c.SourceRef,
			SourceURL:             c.PreviewURL,
			ThumbnailURL:          c.ThumbnailURL,
			Title:                 c.Title,
			Description:           c.Name,
			CandidateScore:        c.Score,
			DiscoveryStatus:       mediamemory.DiscoverySearched,
			MaterializationStatus: mediamemory.MaterializationCold,
			AssetID:               c.AssetID,
		})
	}

	return out, nil
}

// universeFromAllowExternal maps the mediamemory ResolutionSearchPolicy
// AllowExternal flag onto the canonical search.SearchUniverse axis:
//   - AllowExternal=false → SearchCatalog  (no live provider call)
//   - AllowExternal=true  → SearchBlended  (catalog + live providers)
//
// This preserves the historical AllowExternal semantics ("external
// providers may be consulted") while routing through the canonical
// universe filter instead of the advisory hint.
func universeFromAllowExternal(allowExternal bool) search.SearchUniverse {
	if allowExternal {
		return search.SearchBlended
	}
	return search.SearchCatalog
}

// clampLimit normalizes the limit input to the canonical bounds.
// Zero / negative falls back to search.DefaultLimit; over-cap is
// clamped to search.MaxLimit (mirrors search.Aggregator's
// clamping so the adapter does not over-amplify the inner query).
func clampLimit(n int) int {
	if n <= 0 {
		return search.DefaultLimit
	}
	if n > search.MaxLimit {
		return search.MaxLimit
	}
	return n
}
