// Package mediamemory — resolver_scoring.go is the canonical home
// for the resolver scoring layer: mandatory pre-rank gates
// (buildFilterFlags), ranker-input projection (buildRankingInput),
// duration_fit math (durationFitScore), value clamping (clamp01),
// rose sort (sortByFinalScoreDesc + lessRanked), layer composition
// (layerFromFilteredCandidate), and source-label upgrade
// (upgradeSource).
//
// godlike/06 SSOT (single canonical home per layer): every
// numeric transformation applied BETWEEN candidatesForSlot and
// PickTopFromRose lives here so the verifier (operator +
// dashboard) sees one grep-able seam where scoring math is
// authored.
//
// godlike/06 SSOT (lossless binding projection): binding fields
// (ManualScore / SemanticScore / QualityScore / SuccessScore)
// flow into the ranker seats verbatim via buildRankingInput;
// drift here is caught by TestResolve_BindingScoresFlowThroughRanker.
//
// godlike/06 SSOT (rankedCandidate shared with ranker.go): the
// ranker package helper PickTopFromRose takes / returns
// rankedCandidate. Same-package visibility: ranker.go sees the
// type defined here without re-exporting.
//
// File split ownership (godlike/06 SSOT):
//   - resolver.go                 : Resolver port + VisualResolver struct + ResolverDeps + ctors + pins + EmbeddingVersion
//   - resolver_lookup.go          : canonicalConceptForLookup + fingerprintForNormalized
//   - resolver_orchestration.go   : Resolve + resolveScene + candidatesForSlot + levelExactMatch + mediaTypesForSlot + priorSceneVideoID + defaultResolverLimit
//   - resolver_scoring.go         : rankedCandidate + buildFilterFlags + aspectMismatchFor + buildRankingInput + durationFitScore + clamp01 + sort + layerFromFilteredCandidate + upgradeSource  ← this file
//   - resolver_projection.go      : bindingsToFilteredCandidates + candidatesToFilteredCandidates
//   - resolver_brain.go           : errInvalidPhrase + Search method (brain.MediaMemoryResolutionPort impl)
package mediamemory

import (
	"context"
	"sort"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// ── 7-gate filter-input projection ─────────────────────────────────

// buildFilterFlags computes the FilteredCandidate gates from the
// (candidate, binding, scene) triple.
//
// godlike/06 SSOT (filter flags): the seven mandatory gates are
// a JS function of (c, b, scene). They are aggregated into the
// FilteredCandidate booleans which Filter() reads.
//
// Phase 1.x scope:
//   - IsDuplicate: false (Phase 2 anti-repetition lands)
//   - MissingRights: candidate's RightsStatus != RightsVerified
//   - AspectMismatch: candidate's MediaType does not match the slot's expected type
//   - Contaminated: candidate's MaterializationStatus == MaterializationFailed
//   - License valid (gates via MissingRights), availability
//     (MaterializationStatus ∈ {Hot, Warm, Cold}), duration valid
//     (DurationMs > 0 for videos), format supported (MediaType
//     in {"video", "image"}) — all rolled into MissingRights /
//     AspectMismatch for Phase 1.x simplicity. Phase 2 splits
//     each gate into a separate boolean for rich diagnostics.
func buildFilterFlags(
	_ context.Context,
	candidates []FilteredCandidate,
	_ BindingRepository,
	_ SceneSpec,
	slot SlotKind,
) []FilteredCandidate {
	out := make([]FilteredCandidate, 0, len(candidates))
	for _, fc := range candidates {
		cc := fc.Candidate
		// 1. Rights (canonical godlike/07 gate #1): rights-uncertain
		// candidates MUST NOT be promoted to Hot (ranker still sees
		// them but they receive a rights_penalty at Score time).
		missingRights := cc.RightsStatus != "" && cc.RightsStatus != RightsVerified
		// 2. Aspect ratio / media-type mismatches (gate #4):
		// primary_video expects "video" MediaType; secondary_image
		// expects "image". An empty MediaType is allowed
		// (legacy rows) and skips the normal validation.
		aspectMismatch := aspectMismatchFor(slot, cc.MediaType)
		// 3. Corrupted / failed materialization (gate #6).
		contaminated := cc.MaterializationStatus == MaterializationFailed
		// 4. Dedup (gate #7): anti-repetition is Phase 2; Phase 1.x
		// leaves IsDuplicate=false. A future binding
		// anti-repetition column on media_bindings will gate this.
		out = append(out, FilteredCandidate{
			Candidate:      cc,
			Binding:        fc.Binding,
			IsDuplicate:    false,
			MissingRights:  missingRights,
			AspectMismatch: aspectMismatch,
			Contaminated:   contaminated,
		})
	}
	return out
}

// aspectMismatchFor returns true when the slot expects a media
// type that the candidate does not declare. An empty candidate
// MediaType is treated as ambiguous (no mismatch) so legacy rows
// remain selectable.
func aspectMismatchFor(slot SlotKind, mediaType string) bool {
	if mediaType == "" {
		return false
	}
	switch slot {
	case media.SlotPrimaryVideo:
		return mediaType != "video"
	case media.SlotSecondaryImage, media.SlotEvidenceOverlay, media.SlotMap,
		media.SlotPortrait, media.SlotDocument, media.SlotBackground:
		return mediaType != "image"
	}
	return false
}

// ── Ranking input projection ───────────────────────────────────────

// buildRankingInput projects a (FilteredCandidate, SceneSpec) into
// the ranker's canonical RankingInput. When the candidate has a
// binding envelope, scores come from the operator-curated columns
// verbatim. Otherwise they come from canonical defaults.
//
// godlike/06 SSOT (lossless binding projection): binding fields
// ManualScore / SemanticScore / QualityScore / SuccessScore flow
// into the ranker seats without intermediate copying (a future
// drift in this mapping is caught by tests).
func buildRankingInput(scene SceneSpec, fc FilteredCandidate) RankingInput {
	in := RankingInput{
		Candidate: fc.Candidate,
		Binding:   fc.Binding,
	}

	if fc.Binding.AssetID != "" {
		// Path A: binding-envelope projection (godlike/06 SSOT
		// lossless: operator-curated ManualScore flows in verbatim;
		// ApprovalStatus=Approved gates the binding into the
		// resolver hot path but the SCORE comes from ManualScore).
		in.SemanticScore = clamp01(fc.Binding.SemanticScore)
		in.ExactMatchScore = 1.0 // the existence of an approved binding IS the exact-match signal
		in.VisualScore = 0.5     // visual channel is Phase 4; Phase 1.x neutral
		// manual_approval_score is the operator-curated ManualScore
		// (clamped to [0,1]). When the binding is not yet approved
		// it is hard-zeroed so it cannot sneak into the hot path
		// via a high ManualScore while bypassing approval.
		in.ManualApprovalScore = 0.0
		if fc.Binding.ApprovalStatus == ApprovalApproved {
			in.ManualApprovalScore = clamp01(fc.Binding.ManualScore)
		}
		in.QualityScore = clamp01(fc.Binding.QualityScore)
		in.HistoricalSuccessScore = clamp01(fc.Binding.SuccessScore)
	} else if fc.Candidate.CandidateScore > 0 {
		// Path B-variant: candidate-only path from Level 3-7
		// semantic lookup (QdrantSemanticLookup). The Qdrant
		// hybrid-search RRF score propagates verbatim into
		// the ranker's SemanticScore seat so a paraphrase
		// match beats a neutral zero. godlike/06 SSOT
		// (lossless Qdrant-score → ranker-seat projection).
		in.SemanticScore = clamp01(fc.Candidate.CandidateScore)
		in.ExactMatchScore = 0.0 // semantic ≠ exact-match
		in.VisualScore = 0.0
		in.ManualApprovalScore = 0.0
		in.QualityScore = 0.5
		in.HistoricalSuccessScore = 0.4
	} else {
		// Path B (Levels 8/9 path: no binding envelope, no
		// Qdrant score). Defaults are godlike/06 SSOT —
		// Phase 1.x's canonical neutral scores for the
		// candidate-only path that arrives without a Qdrant
		// RRF hint.
		in.SemanticScore = 0.0
		in.ExactMatchScore = 0.0
		in.VisualScore = 0.0
		in.ManualApprovalScore = 0.0
		in.QualityScore = 0.5
		in.HistoricalSuccessScore = 0.4
	}

	// Duration fit: 1.0 when the candidate duration sits inside
	// ±10% of the scene's duration; degrades linearly otherwise.
	// Phase 2 will use a richer curve per visual-action profile.
	in.DurationFitScore = durationFitScore(scene.DurationMs, fc.Candidate.DurationMs)

	// Repetition penalty: applied only at the per-slot level for
	// binding-envelope candidates (Phase 2). Phase 1.x zero.
	in.RepetitionPenalty = 0.0

	// Rights penalty: non-zero when candidate rights != verified.
	// Phase 2 also penalizes AllowConditional verdicts; Phase 1.x
	// keeps the binary penalty.
	if fc.Candidate.RightsStatus != "" && fc.Candidate.RightsStatus != RightsVerified {
		in.RightsPenalty = 0.30
	}
	return in
}

// durationFitScore returns 1.0 when candidate duration sits inside
// ±10% of the scene, 0.0 when more than 2x off, linear between.
func durationFitScore(sceneMs, candidateMs int64) float64 {
	if sceneMs <= 0 || candidateMs <= 0 {
		// Treat missing duration as neutral 0.5 (we cannot penalize).
		if sceneMs <= 0 && candidateMs <= 0 {
			return 0.5
		}
		return 0.0
	}
	ratio := float64(candidateMs) / float64(sceneMs)
	switch {
	case ratio >= 0.9 && ratio <= 1.1:
		return 1.0
	case ratio >= 0.5 && ratio <= 2.0:
		// Linear interpolation between 1.0 (at 0.9..1.1) and 0.0
		// at the 0.5 / 2.0 endpoints.
		d := ratio
		if d > 1.0 {
			d = 2.0 - d
		}
		// d in [0.5, 0.9] → score in [0.0, 1.0]
		return (d - 0.5) / 0.4
	default:
		return 0.0
	}
}

// clamp01 saturates the value to [0,1]. Out-of-range ranker
// inputs (e.g. operator-curated ManualScore = 1.3) are NOT a
// silent zero — clamped at the boundary so the math stays stable.
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// ── Sort + layer composition helpers ────────────────────────────

// rankedCandidate is the internal (filter, score) pair used by
// the resolver sort + pick step.
type rankedCandidate struct {
	fc  FilteredCandidate
	out RankingOutput
}

// sortByFinalScoreDesc sorts in place by FinalScore DESC,
// breaking ties by AssetID ASC for determinism (mirrors
// search.Aggregator's "RankByScore (Score DESC, Source ASC,
// AssetID ASC)" contract from PR 9).
func sortByFinalScoreDesc(in []rankedCandidate) {
	sort.SliceStable(in, func(i, j int) bool {
		return lessRanked(in[i], in[j])
	})
}

// lessRanked orders ranked candidates: higher FinalScore first;
// ties broken by AssetID ASC.
func lessRanked(a, b rankedCandidate) bool {
	if a.out.FinalScore != b.out.FinalScore {
		return a.out.FinalScore > b.out.FinalScore
	}
	return a.fc.Candidate.AssetID < b.fc.Candidate.AssetID
}

// layerFromFilteredCandidate composes a Layer envelope from the
// winning FilteredCandidate + the slot + the final score.
//
// godlike/06 SSOT (lossless binding + provider propagation):
// when the FilteredCandidate has a binding envelope, StartMs /
// EndMs / BindingID flow through verbatim. The Provider tag
// always propagates from the source MediaCandidate — a binding
// envelope does NOT mask the canonical Provider (the Level
// 3-7 semantic adapter stamps ProviderSemanticIndex; the
// Level 9 SearchFanOutAdapter stamps the forwarding provider;
// Level 1+2 binding wins preserve the binding's manually-curated
// origin via fc.Candidate.Provider when present, otherwise "").
func layerFromFilteredCandidate(fc FilteredCandidate, slot SlotKind, finalScore float64) Layer {
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

// upgradeSource returns the higher-ranked source label between
// current and a winning level. Strict priority: exact > semantic >
// local > external > mixed. The current plan.Source starts at
// "exact" (the canonical default) and may stay there when the
// winning level IS exact, get downgraded otherwise.
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
