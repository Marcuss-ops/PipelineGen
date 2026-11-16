// ── FROZEN TEST ORACLE — NOT PART OF THE PRODUCTION BINARY ───────────
// The resolver-side ranking projection as it stood at the TICKET-16 cutover:
// gate flags (buildFilterFlags), ranking-input projection (buildRankingInput),
// duration fit and clamping. These functions fed the Go ranker; production now
// sends the raw candidates to `mediasampler::rank`, which computes the same
// facts in Rust.
//
// They survive only in this _test.go file, where they are the Go half of the
// parity fixtures in contract/media-ranker/v1 — the fixtures are generated
// from this code and asserted against the Rust answer.
package mediamemory

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// buildFilterFlags computes the gate flags from the (candidate, slot) pair.
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
		missingRights := cc.RightsStatus != "" && cc.RightsStatus != RightsVerified
		aspectMismatch := aspectMismatchFor(slot, cc.MediaType)
		contaminated := cc.MaterializationStatus == MaterializationFailed
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

// aspectMismatchFor returns true when the slot expects a media type that the
// candidate does not declare. An empty candidate MediaType is ambiguous and
// therefore accepted.
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

// buildRankingInput projects a (FilteredCandidate, SceneSpec) into the ranker's
// canonical RankingInput.
func buildRankingInput(scene SceneSpec, fc FilteredCandidate) RankingInput {
	in := RankingInput{
		Candidate: fc.Candidate,
		Binding:   fc.Binding,
	}

	if fc.Binding.AssetID != "" {
		in.SemanticScore = clamp01(fc.Binding.SemanticScore)
		in.ExactMatchScore = 1.0
		in.VisualScore = 0.5
		in.ManualApprovalScore = 0.0
		if fc.Binding.ApprovalStatus == ApprovalApproved {
			in.ManualApprovalScore = clamp01(fc.Binding.ManualScore)
		}
		in.QualityScore = clamp01(fc.Binding.QualityScore)
		in.HistoricalSuccessScore = clamp01(fc.Binding.SuccessScore)
	} else if fc.Candidate.CandidateScore > 0 {
		in.SemanticScore = clamp01(fc.Candidate.CandidateScore)
		in.ExactMatchScore = 0.0
		in.VisualScore = 0.0
		in.ManualApprovalScore = 0.0
		in.QualityScore = 0.5
		in.HistoricalSuccessScore = 0.4
	} else {
		in.SemanticScore = 0.0
		in.ExactMatchScore = 0.0
		in.VisualScore = 0.0
		in.ManualApprovalScore = 0.0
		in.QualityScore = 0.5
		in.HistoricalSuccessScore = 0.4
	}

	in.DurationFitScore = durationFitScore(scene.DurationMs, fc.Candidate.DurationMs)
	in.RepetitionPenalty = 0.0
	if fc.Candidate.RightsStatus != "" && fc.Candidate.RightsStatus != RightsVerified {
		in.RightsPenalty = 0.30
	}
	return in
}

// durationFitScore returns 1.0 when candidate duration sits inside ±10% of the
// scene, 0.0 when more than 2x off, linear between.
func durationFitScore(sceneMs, candidateMs int64) float64 {
	if sceneMs <= 0 || candidateMs <= 0 {
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
		d := ratio
		if d > 1.0 {
			d = 2.0 - d
		}
		return (d - 0.5) / 0.4
	default:
		return 0.0
	}
}

// clamp01 saturates the value to [0,1].
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
