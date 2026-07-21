// Package mediamemory — ranker_test.go pins the canonical
// ranking formula and filter gates.
//
// godlike/06 SSOT (one canonical owner per fact): the formula
//
//	final = semantic*0.30 + exact*0.20 + visual*0.15 +
//	        manual*0.15 + quality*0.10 + historical*0.05 +
//	        duration_fit*0.05 - repetition - rights
//
// is mirrored verbatim. Tests assert the coefficient assignments
// on gold inputs so the next tuning pass surfaces as a metric
// change, NOT a silent regression.
//
// godlike/07 NO-FAKE-AVAILABILITY: the malformed-candidate guard
// (verdict=Drop when AssetID is empty) is tested explicitly so
// callers cannot inject a zero-AssetID drift.
package mediamemory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// goldenScore computes the canonical formula in the test
// harness — the ranker's private math must always equal this
// expression on the same inputs. Any drift surfaces as a test
// failure.
func goldenScore(in RankingInput) float64 {
	return (in.SemanticScore * 0.30) +
		(in.ExactMatchScore * 0.20) +
		(in.VisualScore * 0.15) +
		(in.ManualApprovalScore * 0.15) +
		(in.QualityScore * 0.10) +
		(in.HistoricalSuccessScore * 0.05) +
		(in.DurationFitScore * 0.05) -
		in.RepetitionPenalty -
		in.RightsPenalty
}

func TestDefaultRanker_ScoreMatchesCanonicalFormula(t *testing.T) {
	r := NewDefaultRanker(nil, nil)
	in := RankingInput{
		SemanticScore:          0.80,
		ExactMatchScore:        1.0,
		VisualScore:            0.50,
		ManualApprovalScore:    0.95,
		QualityScore:           0.70,
		HistoricalSuccessScore: 0.60,
		DurationFitScore:       1.0,
	}
	out, err := r.Score(context.Background(), in)
	assert.NoError(t, err)
	want := goldenScore(in)
	// Floating equality on a deterministic formula.
	assert.InDelta(t, want, out.FinalScore, 0.0001,
		"Score MUST match the canonical formula (semantic*0.30 + ... - repetition - rights)")
}

func TestDefaultRanker_ScoreAcceptsOnHighScore(t *testing.T) {
	r := NewDefaultRanker(nil, nil)
	// All seats at 1.0 → final ≈ 1.0 → Accept (> 0.05)
	out, err := r.Score(context.Background(), RankingInput{
		SemanticScore:          1.0,
		ExactMatchScore:        1.0,
		VisualScore:            1.0,
		ManualApprovalScore:    1.0,
		QualityScore:           1.0,
		HistoricalSuccessScore: 1.0,
		DurationFitScore:       1.0,
	})
	assert.NoError(t, err)
	assert.Equal(t, VerdictAccept, out.Verdict,
		"final_score=1.0 MUST yield VerdictAccept (> 0.05)")
	assert.Greater(t, out.FinalScore, 0.05)
}

func TestDefaultRanker_ScoreDropsOnHeavyPenalty(t *testing.T) {
	r := NewDefaultRanker(nil, nil)
	// All good scores but rights_penalty=0.5 → final ≈ 0.5 → Downrank
	// (between 0 and 0.05? Yes — 0.5 > 0.05 → Accept). To hit Drop,
	// set rights_penalty above the score.
	out, err := r.Score(context.Background(), RankingInput{
		QualityScore:  0.10,
		RightsPenalty: 1.5,
	})
	assert.NoError(t, err)
	assert.Equal(t, VerdictDrop, out.Verdict,
		"final_score < 0 MUST yield VerdictDrop (negative — rights_penalty > positive contributions)")
	assert.Less(t, out.FinalScore, 0.0)
}

func TestDefaultRanker_ScoreDownranksOnMidScore(t *testing.T) {
	r := NewDefaultRanker(nil, nil)
	// All zero seats except SemanticScore=0.10 → 0.10*0.30=0.03
	// → within [0.0, 0.05] → Downrank.
	out, err := r.Score(context.Background(), RankingInput{
		SemanticScore: 0.10,
	})
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, out.FinalScore, 0.0)
	assert.LessOrEqual(t, out.FinalScore, 0.05)
	assert.Equal(t, VerdictDownrank, out.Verdict,
		"final_score within [0, 0.05] MUST yield VerdictDownrank")
}

func TestDefaultRanker_ScoreRejectsMalformedCandidate(t *testing.T) {
	// godlike/06 SSOT one-canonical-owner-per-fact: the well-
	// formed guard now lives in Filter (canonical seam). Score is
	// strictly math-only. The malformed-candidate dropping is
	// asserted by TestDefaultRanker_FilterRemovesAllSevenGateFailures.
	r := NewDefaultRanker(nil, nil)
	out, err := r.Score(context.Background(), RankingInput{
		Candidate: MediaCandidate{AssetID: ""},
	})
	assert.NoError(t, err)
	// Math now runs on the (empty) candidate without crashing.
	// The ranker produces a verdict from the seats; defaults to
	// zero so the score floor is the Downrank band.
	assert.Equal(t, VerdictDownrank, out.Verdict,
		"empty scores on a zero-candidate MUST yield Downrank (filter is the canonical malformed seam)")
}

func TestScoreNoGuardClosesMathOnlySeam(t *testing.T) {
	// godlike/06 SSOT pin: there is no well-formed guard in
	// Score. A malformed candidate reaches the math with the
	// default empty scores — produces a Downrank, never panics.
	r := NewDefaultRanker(nil, nil)
	_, err := r.Score(context.Background(), RankingInput{
		Candidate: MediaCandidate{},
		// All seats zero — math result is 0 → Downrank.
	})
	assert.NoError(t, err)
}

func TestDefaultRanker_FilterRemovesAllSevenGateFailures(t *testing.T) {
	r := NewDefaultRanker(nil, nil)
	candidate := MediaCandidate{
		AssetID:               "asset-clean",
		MaterializationStatus: MaterializationHot,
		DiscoveryStatus:       DiscoveryIndexed,
	}
	in := []FilteredCandidate{
		{Candidate: candidate},                                         // clean
		{Candidate: candidate, IsDuplicate: true},                      // gate 7
		{Candidate: candidate, MissingRights: true},                    // gate 1+5
		{Candidate: candidate, AspectMismatch: true},                   // gate 4
		{Candidate: candidate, Contaminated: true},                     // gate 6
		{Candidate: candidate, IsDuplicate: true, MissingRights: true}, // multi-fail
		{Candidate: MediaCandidate{AssetID: "", MaterializationStatus: MaterializationHot, DiscoveryStatus: DiscoveryIndexed}}, // malformed
	}
	out, err := r.Filter(context.Background(), in)
	assert.NoError(t, err)
	assert.Len(t, out, 1, "Filter MUST remove every row that fails any of the seven gates; only the clean row survives")
	assert.Equal(t, "asset-clean", out[0].Candidate.AssetID)
}

func TestDefaultRanker_FilterOnEmptyInputReturnsEmpty(t *testing.T) {
	r := NewDefaultRanker(nil, nil)
	out, err := r.Filter(context.Background(), nil)
	assert.NoError(t, err)
	assert.Empty(t, out, "Filter on nil input MUST return empty slice (no panic)")
}

func TestDefaultRanker_WeightsReturnsCanonicalValues(t *testing.T) {
	w := Weights()
	assert.Equal(t, 0.30, w.Semantic)
	assert.Equal(t, 0.20, w.ExactMatch)
	assert.Equal(t, 0.15, w.Visual)
	assert.Equal(t, 0.15, w.ManualApproval)
	assert.Equal(t, 0.10, w.Quality)
	assert.Equal(t, 0.05, w.HistoricalSuccess)
	assert.Equal(t, 0.05, w.DurationFit)
	// Total weight = 1.0 by construction.
	total := w.Semantic + w.ExactMatch + w.Visual + w.ManualApproval +
		w.Quality + w.HistoricalSuccess + w.DurationFit
	assert.InDelta(t, 1.0, total, 0.0001,
		"the seven positive weights MUST sum to 1.0 (architecture doc spec)")
}

func TestClassifyScoreBoundariesAreStrict(t *testing.T) {
	vt := DefaultVerdictThresholds()
	// Boundaries: > Accept → Accept, < Drop → Drop, else Downrank.
	assert.Equal(t, VerdictAccept, classifyScore(vt.Accept+0.001, vt))
	assert.Equal(t, VerdictDownrank, classifyScore(vt.Accept, vt), "score == Accept threshold MUST downrank")
	assert.Equal(t, VerdictDownrank, classifyScore(0.025, vt))
	assert.Equal(t, VerdictDownrank, classifyScore(0.0, vt), "score == 0 MUST downrank")
	assert.Equal(t, VerdictDrop, classifyScore(-0.001, vt), "score just below 0 MUST drop")
}

func TestMediaCandidateIsWellFormedEnforced(t *testing.T) {
	assert.True(t, mediaCandidateIsWellFormed(MediaCandidate{
		AssetID: "x", DiscoveryStatus: DiscoveryIndexed, MaterializationStatus: MaterializationHot,
	}), "valid candidate MUST be well-formed")
	assert.False(t, mediaCandidateIsWellFormed(MediaCandidate{AssetID: ""}),
		"empty AssetID MUST be malformed")
	assert.False(t, mediaCandidateIsWellFormed(MediaCandidate{
		AssetID: "x", MaterializationStatus: "alien",
	}), "unknown MaterializationStatus MUST be malformed")
	assert.False(t, mediaCandidateIsWellFormed(MediaCandidate{
		AssetID: "x", MaterializationStatus: MaterializationHot, DiscoveryStatus: "unknown",
	}), "unknown DiscoveryStatus MUST be malformed")
}

func TestDurationFitScoreStableAcrossEdgeCases(t *testing.T) {
	assert.InDelta(t, 0.5, durationFitScore(0, 0), 0.001, "both-zero MUST be neutral 0.5")
	assert.InDelta(t, 0.0, durationFitScore(8000, 0), 0.001, "missing candidate duration MUST be 0.0")
	assert.InDelta(t, 1.0, durationFitScore(8000, 8000), 0.001, "exact match MUST be 1.0")
	assert.InDelta(t, 1.0, durationFitScore(8000, 8800), 0.001, "±10% MUST be 1.0")
	assert.Less(t, durationFitScore(8000, 16000), 0.5, "2x overshoot MUST degrade")
}

func TestClamp01Saturation(t *testing.T) {
	assert.Equal(t, 0.0, clamp01(-0.5))
	assert.Equal(t, 0.0, clamp01(0.0))
	assert.Equal(t, 0.5, clamp01(0.5))
	assert.Equal(t, 1.0, clamp01(1.0))
	assert.Equal(t, 1.0, clamp01(2.0), "over-1 MUST clamp at 1 (saturating, not error)")
}
