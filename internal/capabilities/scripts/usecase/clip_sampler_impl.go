// Package scripts \u2014 clip_sampler_impl.go is the canonical single
// implementation of the ClipSampler port.
//
// godlike/06 SSOT: this file is the SOLE owner of the
// deduplication + selection + coverage + gate-audit policy
// consumed by the search, catalog, and curate resolvers.
//
// FASE-8 (July 2026): the impl runs ALL 10 audit gates per
// candidate and accumulates a GateProvenanceRecord for EVERY
// evaluation (pass or fail). The Provenance slice forms the
// full audit trail an operator inspects post-hoc.
//
// godlike/07 NO-FAKE-AVAILABILITY: contract violations return
// typed error envelopes (SourceResolutionError). Coverage-gate
// failure returns nil result AND a non-nil error envelope. The
// sampler NEVER synthesises defaults to mask sparse candidates;
// missing-metadata candidates fail-loud (gate evaluator appends
// `Passed=false` with explicit reason). Resolver-side enrichment
// is the FASE-9 work that lets real resolvers feed rich
// candidates to this sampler \u2014 the sampler's job is to gate,
// not to enrich.
package usecase

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// defaultClipSampler is the canonical sampler implementation.
// Held by ClipSamplerRegistry (godlike/06 SSOT: single instance).
type defaultClipSampler struct {
	// gates is the canonical 10-gate list. Order is the audit
	// contract (canonical even for null-evaluation outcomes).
	gates []SamplerGate
}

// NewDefaultClipSampler returns the canonical impl with the
// 10-gate audit pipeline wired in. This is the only
// constructor; godlike/06 SSOT forbids alternative impls.
func NewDefaultClipSampler() ports.ClipSampler {
	return &defaultClipSampler{gates: defaultGates()}
}

// Select applies the canonical dedup + limit + coverage + 10-gate
// audit policy. Semantics, in order:
//
//  1. Validate request: Limit must be > 0; fail-closed otherwise.
//  2. For each candidate (in caller-supplied order):
//     a. Skip empty ClipIDs (defensive; ports should not emit).
//     b. Drop candidates with Score < req.MinScore.
//     c. Skip duplicates (seen[] lookup on ClipID).
//     d. Run ALL 10 gates; emit one GateProvenanceRecord per gate.
//     e. If ANY gate failed, drop the candidate.
//     f. Otherwise, append to ClipIDs + SearchItems.
//     g. Stop once len(ClipIDs) == req.Limit.
//  3. If MinCoverage > 0 and len(ClipIDs)/req.Limit < MinCoverage:
//     return (ClipSamplerResult{}, ErrCoverageGate).
//
// Provenance writes happen for EVERY (candidate, gate) regardless
// of pass/fail; the dropped candidates leave a paper trail so a
// post-hoc audit can answer "why did candidate X fail the plan?".
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
	provenance := scriptpkg.SamplerProvenance{
		Records: make([]scriptpkg.GateProvenanceRecord, 0, len(candidates)*len(s.gates)),
	}

	gateInput := ClipSamplerGateInput{
		Slot:               req.Slot,
		PreviousSelections: req.PreviousSelections,
		CallingSource:      req.CallingSource,
		SourceTextLength:   len(req.Query),
	}

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

		gateInput.Candidate = c
		allPassed := true
		for _, g := range s.gates {
			passed, reason := g.Evaluate(gateInput)
			provenance.Records = append(provenance.Records, scriptpkg.GateProvenanceRecord{
				SlotRef:     req.SlotRef,
				CandidateID: c.ClipID,
				GateName:    g.Name(),
				Passed:      passed,
				Reason:      reason,
			})
			if !passed {
				allPassed = false
			}
		}
		if !allPassed {
			// Drop the candidate but preserve the audit trail row.
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

	// Coverage gate (still applies after the 10-gate filter):
	if req.MinCoverage > 0 && req.Limit > 0 {
		coverage := float64(len(clipIDs)) / float64(req.Limit)
		if coverage < req.MinCoverage {
			return ports.ClipSamplerResult{Provenance: provenance}, &scriptpkg.SourceResolutionError{
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
		Provenance:  provenance,
	}, nil
}
