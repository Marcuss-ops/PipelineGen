// Package mediamemory — resolver_scoring.go is the resolver's ranking seam
// after the TICKET-16 cutover: it projects a slot pool across the PoolRanker
// boundary, maps the recorded ranking back onto the resolver's envelopes, and
// composes the winning Layer.
//
// There is no scoring math left in this package's production build. The
// canonical weights, the seven gates, the verdict bands, the repetition
// penalty and the diversity pick live in `mediasampler::rank` (Rust) and are
// reached through PoolRanker. The Go oracle that used to live here (weights,
// gates, duration fit, sort, pick) moved to `ranker_oracle_test.go`,
// `ranker_oracle_weights_test.go`, `ranker_oracle_gates_test.go` and
// `ranker_oracle_repetition_test.go`, where it is compiled only into test
// binaries so it can generate and check contract/media-ranker/v1.
//
// File split ownership (godlike/06 SSOT):
//   - resolver.go                 : Resolver port + VisualResolver struct + ResolverDeps + ctors + pins + EmbeddingVersion
//   - resolver_lookup.go          : canonicalConceptForLookup + fingerprintForNormalized
//   - resolver_orchestration.go   : Resolve + resolveScene + candidatesForSlot + levelExactMatch + mediaTypesForSlot + priorSceneVideoID + defaultResolverLimit
//   - resolver_scoring.go         : rankSlotPool + layerFromRanked + upgradeSource  ← this file
//   - resolver_projection.go      : bindingsToFilteredCandidates + candidatesToFilteredCandidates
//   - resolver_brain.go           : errInvalidPhrase + Search method (brain.MediaMemoryResolutionPort impl)
package mediamemory

import (
	"context"
	"fmt"
	"strconv"
)

// rankSlotPool asks the canonical ranker for one slot pool.
//
// godlike/07 NO-FAKE-AVAILABILITY: a missing ranker, a worker failure or an
// answer that names a candidate outside the pool is a typed error — the caller
// turns it into a per-scene warning. It is never a silent empty ranking and
// never a reason to score locally.
func (r *VisualResolver) rankSlotPool(
	ctx context.Context,
	scene SceneSpec,
	slot SlotKind,
	candidates []FilteredCandidate,
	prevVideoID string,
) (PoolRankOutcome, error) {
	if r.poolRanker == nil {
		return PoolRankOutcome{}, fmt.Errorf(
			"pool ranker is not configured: the canonical ranker lives in mediasampler (slot_kind=%q)", slot)
	}

	request := PoolRankRequest{
		SceneDurationMS: scene.DurationMs,
		PrevVideoID:     prevVideoID,
		SlotKind:        slot,
		Candidates:      make([]PoolRankCandidate, 0, len(candidates)),
	}
	known := make(map[string]struct{}, len(candidates))
	for index, fc := range candidates {
		key := poolRankKey(index, fc.Candidate.ID)
		known[key] = struct{}{}
		request.Candidates = append(request.Candidates,
			PoolRankCandidate{Key: key, Filtered: fc})
	}

	outcome, err := r.poolRanker.RankPool(ctx, request)
	if err != nil {
		return PoolRankOutcome{}, err
	}
	for _, scored := range outcome.Ranked {
		if _, ok := known[scored.CandidateID]; !ok {
			return PoolRankOutcome{}, fmt.Errorf(
				"ranker answered with candidate %q, which was not part of the %q pool",
				scored.CandidateID, slot)
		}
	}
	if outcome.Winner != nil {
		found := false
		for _, scored := range outcome.Ranked {
			if scored.CandidateID == outcome.Winner.CandidateID {
				found = true
				break
			}
		}
		if !found {
			return PoolRankOutcome{}, fmt.Errorf(
				"ranker named winner %q outside its own ranking (slot_kind=%q)",
				outcome.Winner.CandidateID, slot)
		}
	}
	return outcome, nil
}

// poolRankKey is the resolver's identity for one pool entry. The ordinal is
// part of the key on purpose: binding-derived candidates carry no
// MediaCandidate.ID, and a raw ID could repeat when one asset appears twice.
func poolRankKey(index int, candidateID string) string {
	return strconv.Itoa(index) + "|" + candidateID
}

// layerFromRanked composes a Layer envelope from the winning candidate + the
// slot + the final score decided by the ranker.
//
// godlike/06 SSOT (lossless binding + provider propagation): when the
// candidate carries a binding envelope, StartMs / EndMs / BindingID flow
// through verbatim. The Provider tag always propagates from the source
// MediaCandidate — a binding envelope does NOT mask the canonical Provider.
func layerFromRanked(fc FilteredCandidate, slot SlotKind, finalScore float64) Layer {
	layer := Layer{
		Slot:           slot,
		AssetID:        fc.Candidate.AssetID,
		CandidateID:    fc.Candidate.ID,
		CandidateScore: finalScore,
		Provider:       fc.Candidate.Provider,
	}
	if fc.Binding.AssetID != "" {
		layer.BindingID = fc.Binding.ID
		layer.StartMs = fc.Binding.StartMs
		layer.EndMs = fc.Binding.EndMs
	}
	return layer
}

// upgradeSource returns the higher-ranked source label between current and a
// winning level. Strict priority: exact > semantic > local > external > mixed.
// The current plan.Source starts at "exact" (the canonical default) and may
// stay there when the winning level IS exact, get downgraded otherwise.
func upgradeSource(current, winning string) string {
	if winning == "" {
		return current
	}
	rank := map[string]int{
		"exact":    4,
		"semantic": 3,
		"local":    2,
		"external": 1,
		"mixed":    0,
	}
	if rank[winning] > rank[current] {
		return winning
	}
	if current == "" {
		return winning
	}
	return current
}
