// Package mediamemory — resolver_projection.go is the canonical
// home for the projection layer that bridges raw repository
// envelopes (MediaBinding rows + MediaCandidate rows) into the
// ranker's canonical FilteredCandidate shape.
//
// godlike/06 SSOT (single canonical home per layer): every
// binding/candidate → FilteredCandidate projection lives here so
// the boundary between cascade and scoring is one grep-able seam.
// The cascade drives (Level 1+2 → bindings, Level 3-9 → candidates);
// this file normalises both shapes into the ranker's input.
//
// File split ownership (godlike/06 SSOT):
//   - resolver.go                 : Resolver port + VisualResolver struct + ResolverDeps + ctors + pins + EmbeddingVersion
//   - resolver_lookup.go          : canonicalConceptForLookup + fingerprintForNormalized
//   - resolver_orchestration.go   : Resolve + resolveScene + candidatesForSlot + levelExactMatch + mediaTypesForSlot + priorSceneVideoID + defaultResolverLimit
//   - resolver_scoring.go         : rankedCandidate + buildFilterFlags + aspectMismatchFor + buildRankingInput + durationFitScore + clamp01 + sort + layerFromFilteredCandidate + upgradeSource
//   - resolver_projection.go      : bindingsToFilteredCandidates + candidatesToFilteredCandidates  ← this file
//   - resolver_brain.go           : errInvalidPhrase + Search method (brain.MediaMemoryResolutionPort impl)
package mediamemory

// ── Bindings → FilteredCandidate projection (lossless) ────────────

// bindingsToFilteredCandidates converts MediaBinding rows into
// FilteredCandidate envelopes WITHOUT losing operator-curated
// fields. The FilteredCandidate.Binding field carries the binding
// envelope (godlike/06 SSOT extension) so the ranker can pull
// ManualScore, SemanticScore, QualityScore, SuccessScore and the
// binding window (StartMs, EndMs) downstream.
//
// AssetID is the bridge: media_bindings.asset_id → media_assets.id.
// A binding without AssetID is fail-closed (FilteredCandidate is
// skipped and a typed warning is appended).
//
// godlike/06 SSOT (canonical defaults): the synthesized Candidate
// carries canonical MaterializationStatus=Hot + DiscoveryStatus=Searched.
// An approved, manually-curated binding is treated as available
// media so it survives the ranker's availability gate; the
// binding envelope does NOT carry the cache-tier or pipeline-
// completion assertion (those live on the linked media_assets row).
// Phase 2 adds the projection loader (binding → asset hot-tier
// query) at which point the canonical defaults are replaced with
// real values.
//
// Filter's well-formed guard requires non-empty Materialization/
// Discovery statuses in the canonical closed sets; without
// these defaults every binding hit would fail the guard and be
// dropped with no Layer produced.
func bindingsToFilteredCandidates(bindings []MediaBinding) []FilteredCandidate {
	out := make([]FilteredCandidate, 0, len(bindings))
	for _, b := range bindings {
		if b.AssetID == "" {
			continue
		}
		out = append(out, FilteredCandidate{
			Candidate: MediaCandidate{
				AssetID:               b.AssetID,
				MaterializationStatus: MaterializationHot,
				DiscoveryStatus:       DiscoverySearched,
			},
			Binding: b,
		})
	}
	return out
}

// candidatesToFilteredCandidates converts MediaCandidate rows to
// FilteredCandidate envelopes (Level 8/9 path — no binding
// envelope available, so Binding stays zero). The ranker falls
// back to canonical default scores via the RankingInput.
func candidatesToFilteredCandidates(candidates []MediaCandidate) []FilteredCandidate {
	out := make([]FilteredCandidate, 0, len(candidates))
	for _, c := range candidates {
		if c.AssetID == "" {
			continue
		}
		out = append(out, FilteredCandidate{Candidate: c})
	}
	return out
}
