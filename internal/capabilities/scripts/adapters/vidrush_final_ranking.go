package adapters

// Final ranking policy for candidate windows (VidRush Fases 9–10).
//
// The small-model reranker contributes ONLY the semantic component; every
// other component and the weighted combination are deterministic, so the
// same candidate set always scores identically on replay:
//
//	final = semantic*0.40 + transcript*0.25 + visual*0.15
//	      + technical*0.10 + durationFit*0.05 + providerTrust*0.05
//
// The reranker sees meaning only (segment text, topic, transcript excerpt);
// it never sees provider plumbing and never initiates a download. A failed
// or absent reranker degrades gracefully: the deterministic score still
// ranks with the semantic component carried by the transcript match.

import (
	"math"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// FinalScoreWeights is the fixed deterministic final-score policy. The
// weights sum to 1.0 and are part of the ranking contract: changing them
// changes the replay-identical score contract.
type FinalScoreWeights struct {
	Semantic      float64
	Transcript    float64
	Visual        float64
	Technical     float64
	DurationFit   float64
	ProviderTrust float64
}

// defaultFinalScoreWeights is the fixed final-score policy:
// semantic 40%, transcript 25%, visual 15%, technical 10%,
// duration fit 5%, provider trust 5%.
var defaultFinalScoreWeights = FinalScoreWeights{
	Semantic:      0.40,
	Transcript:    0.25,
	Visual:        0.15,
	Technical:     0.10,
	DurationFit:   0.05,
	ProviderTrust: 0.05,
}

func clampUnit(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

// durationFitScore measures how well a candidate window's duration fits the
// beat's timing budget: 1.0 at an exact fit, decaying linearly to 0.0 at
// half or double the target duration. Zero target means no information and
// scores a neutral 1.0 so the component never penalizes without a budget.
func durationFitScore(durationMs, targetMs int64) float64 {
	if targetMs <= 0 {
		return 1.0
	}
	if durationMs <= 0 {
		return 0.0
	}
	ratio := float64(durationMs) / float64(targetMs)
	if ratio > 2 {
		ratio = 2
	}
	return clampUnit(1.0 - math.Abs(ratio-1.0))
}

// providerTrustScore maps the candidate's provider trust signals to [0, 1]:
// a verified rights status contributes the base trust; a known provider
// reliability refines it. Unknown signals stay neutral-positive (0.5 base)
// so trust never dominates the semantic components.
func providerTrustScore(candidate scriptpkg.SegmentAssetCandidate) float64 {
	trust := 0.5
	if strings.EqualFold(strings.TrimSpace(candidate.RightsStatus), "verified") {
		trust = 0.9
	}
	reliability := clampUnit(candidate.ProviderReliability)
	if candidate.ProviderReliability != 0 || reliability > 0 {
		trust = 0.6*trust + 0.4*reliability
	}
	return clampUnit(trust)
}

// FinalScoreComponents exposes the per-component inputs for observability
// and tests: the deterministic scorer is transparent by construction.
type FinalScoreComponents struct {
	Semantic      float64
	Transcript    float64
	Visual        float64
	Technical     float64
	DurationFit   float64
	ProviderTrust float64
}

// FinalScoreWeightsFor returns the fixed weight policy.
func FinalScoreWeightsFor() FinalScoreWeights {
	return defaultFinalScoreWeights
}

// FinalScore is the deterministic weighted combination over clamped
// components. Same components in, same score out — replay-identical.
func FinalScore(components FinalScoreComponents) float64 {
	w := defaultFinalScoreWeights
	return clampUnit(w.Semantic*clampUnit(components.Semantic)) +
		clampUnit(w.Transcript*clampUnit(components.Transcript)) +
		clampUnit(w.Visual*clampUnit(components.Visual)) +
		clampUnit(w.Technical*clampUnit(components.Technical)) +
		clampUnit(w.DurationFit*clampUnit(components.DurationFit)) +
		clampUnit(w.ProviderTrust*clampUnit(components.ProviderTrust))
}
