// Package scripts \u2014 clip_sampler_impl.go is the canonical single
// implementation of the ClipSampler port.
//
// godlike/06 SSOT: this file is the SOLE owner of the
// deduplication + selection + coverage policy consumed by the
// search, catalog, and curate resolvers. The previous design had
// three independent copies of this loop (search resolver lines
// ~88-122, catalog resolver lines ~75-110, curate resolver
// lines ~127-156 embedded inline). The move-only refactor
// consolidates them here; the three resolvers normalize their
// raw candidates into []ports.ClipSamplerCandidate, call
// Select(req, candidates), and consume the result. There is no
// resolver-local copy of this loop anymore.
//
// godlike/07 NO-FAKE-AVAILABILITY: contract violation (Limit
// <= 0) returns a typed error envelope rather than a degraded
// no-op. Coverage-gate failure returns a nil result AND a
// non-nil error envelope — equivalent to the original resolver
// behaviour (which returned `nil, err` and discarded any partial
// state). FASE-7 fix-up: drop the partial-result field under
// coverage failure so the surface is byte-equivalent to the
// move-only contract.
package usecase

import (
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
)

// defaultClipSampler is the canonical sampler implementation.
// Exposed via NewDefaultClipSampler(); held by ClipSamplerRegistry.
// The struct is intentionally empty \u2014 the policy is method-local
// pure logic with no internal state.
type defaultClipSampler struct{}

// NewDefaultClipSampler returns the canonical impl. This is the
// only constructor; godlike/06 SSOT forbids alternative impls.
func NewDefaultClipSampler() ports.ClipSampler {
	return &defaultClipSampler{}
}

// Select applies the canonical dedup + limit + coverage policy.
// Semantics, in order:
//   1. Validate request: Limit must be > 0; fail-closed otherwise.
//   2. For each candidate (in caller-supplied order):
//      a. Skip empty ClipIDs (defensive; ports should not emit).
//      b. Drop candidates with Score < req.MinScore.
//      c. Skip duplicates (seen[] lookup on ClipID).
//      d. Append to ClipIDs + SearchItems (with Source verbatim).
//      e. Stop once len(ClipIDs) == req.Limit.
//   3. If MinCoverage > 0 and len(ClipIDs)/req.Limit < MinCoverage:
//      return (partial result, ErrCoverageGate).
//
// The per-candidate order is caller-controlled, so search/catalog
// can pass semantic-similarity order and curate can pass hits-first
// then hint-only order \u2014 all through the same impl.
func (s *defaultClipSampler) Select(
	req ports.ClipSamplerRequest,
	candidates []ports.ClipSamplerCandidate,
) (ports.ClipSamplerResult, error) {
	if req.Limit <= 0 {
		return ports.ClipSamplerResult{}, &scriptpkg.SourceResolutionError{
			SourceType:  req.SourceType,
			Query:       req.Query,
			ResultCount: 0,
			Inner:       fmt.Errorf("clip sampler: limit must be > 0 (calling_source=%s)", req.CallingSource),
		}
	}

	seen := make(map[string]struct{}, req.Limit)
	clipIDs := make([]string, 0, req.Limit)
	items := make([]scriptpkg.SearchResultItem, 0, req.Limit)

	for _, c := range candidates {
		if c.ClipID == "" {
			continue
		}
		if req.MinScore > 0 && c.Score < req.MinScore {
			continue
		}
		if _, dup := seen[c.ClipID]; dup {
			continue
		}
		seen[c.ClipID] = struct{}{}
		clipIDs = append(clipIDs, c.ClipID)
		items = append(items, scriptpkg.SearchResultItem{
			ClipID: c.ClipID,
			Name:   c.Name,
			Score:  c.Score,
			Source: c.Source,
		})
		if len(clipIDs) >= req.Limit {
			break
		}
	}

	if len(clipIDs) == 0 {
		// No selection \u2014 mirror the original resolvers' behavior
		// (search/catalog return SourceResolutionError; curate
		// returns ErrCurateNoClips). The lift intentionally does
		// NOT pick one; callers receive clipIDs=nil and decide.
	}

	if req.MinCoverage > 0 && req.Limit > 0 {
		coverage := float64(len(clipIDs)) / float64(req.Limit)
		if coverage < req.MinCoverage {
			return ports.ClipSamplerResult{}, &scriptpkg.SourceResolutionError{
				SourceType:  req.SourceType,
				Query:       req.Query,
				ResultCount: len(clipIDs),
				Inner: fmt.Errorf(
					"clip sampler: coverage %.2f below required minimum %.2f (calling_source=%s)",
					coverage, req.MinCoverage, req.CallingSource),
			}
		}
	}

	return ports.ClipSamplerResult{
		ClipIDs:     clipIDs,
		SearchItems: items,
	}, nil
}
