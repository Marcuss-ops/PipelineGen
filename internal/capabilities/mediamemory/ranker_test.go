// Package mediamemory — ranker_test.go is the anti-repetition
// unit-test surface for PopulateRepetitionPenalty.
//
// godlike/06 SSOT (formula pin): the only penalty component that
// survives the Fase 2.3 retirement is
// SameVideoInConsecutiveScenePenalty. It is derived from prevVideoID
// alone — no project-history read — so these tests pin that contract
// without a UsageEvent seam.
//
// godlike/07 NO-FAKE-AVAILABILITY: the tests use deterministic
// inputs (no RNG, no time.Now, no env-var seams) so the formula's
// monotonic behaviour across repetition is racy-free under -race.
//
// FASE-2.3 RETIREMENT (2026-09-20): the same-asset, channel-
// saturation and channel-recency components were deleted along with
// their exclusive input source (the UsageEvent log, whose only writer
// was a FeedbackService composition that was never constructed). The
// assertions that pinned those three components went with them;
// keeping them would have pinned arithmetic no production path could
// reach.
package mediamemory

import (
	"testing"
)

// makeRankingInput returns a minimal RankingInput envelope with the
// candidate identity required for the penalty calculation. Tests pin
// scores to 0 so the penalty alone drives FinalScore comparisons.
func makeRankingInput(assetID, channelID, videoID string) RankingInput {
	return RankingInput{
		Candidate: MediaCandidate{
			AssetID:   assetID,
			ChannelID: channelID,
			VideoID:   videoID,
		},
	}
}

// ── Consecutive-source penalty ────────────────────────────────

// TestRanker_PopulateRepetitionPenalty_ConsecutiveSource pins the
// SPEC's "stessa sorgente consecutiva" trigger:
//  1. Candidate whose video_id matches prevVideoID -> +0.3.
//  2. Candidate with a different video_id -> 0 (scores normally).
//  3. The AssetID fallback still fires for candidates with no
//     VideoID, so reused bindings stay suppressed until the Fase 3
//     linker populates sub-clip identity.
func TestRanker_PopulateRepetitionPenalty_ConsecutiveSource(t *testing.T) {
	t.Parallel()

	const prevVideo = "video-001"
	in := []RankingInput{
		makeRankingInput("asset-same-source", "channel-A", prevVideo),
		makeRankingInput("asset-fresh", "channel-B", "video-fresh"),
		// No VideoID set -> extractCandidateVideoID falls back to
		// AssetID, which matches prevVideoID here.
		makeRankingInput(prevVideo, "channel-C", ""),
	}

	out := PopulateRepetitionPenalty(in, prevVideo)

	if len(out) != 3 {
		t.Fatalf("PopulateRepetitionPenalty returned %d inputs, want 3", len(out))
	}

	a := out[0]
	wantConsecutive := DefaultRepetitionPenaltyWeights().SameVideoInConsecutiveScenePenalty
	if !floatNear(a.RepetitionPenalty, wantConsecutive, 1e-9) {
		t.Fatalf("candidate A RepetitionPenalty = %v, want ~%v (±1e-9; consecutive-source)",
			a.RepetitionPenalty, wantConsecutive)
	}

	if !floatNear(out[1].RepetitionPenalty, 0.0, 1e-9) {
		t.Fatalf("candidate B (fresh source) RepetitionPenalty = %v, want 0 (±1e-9)",
			out[1].RepetitionPenalty)
	}

	if !floatNear(out[2].RepetitionPenalty, wantConsecutive, 1e-9) {
		t.Fatalf("candidate C (AssetID fallback) RepetitionPenalty = %v, want ~%v (±1e-9)",
			out[2].RepetitionPenalty, wantConsecutive)
	}
}

// ── Nil / empty seams ─────────────────────────────────────────

// TestRanker_PopulateRepetitionPenalty_NilAndEmptySeams pins the
// godlike/07 NO-FAKE-AVAILABILITY boundary:
//   - nil inputs -> empty outputs (no panic).
//   - empty prevVideoID -> no consecutive penalty fires (first
//     scene of a project has no prior scene to compare against).
func TestRanker_PopulateRepetitionPenalty_NilAndEmptySeams(t *testing.T) {
	t.Parallel()

	if out := PopulateRepetitionPenalty(nil, ""); len(out) != 0 {
		t.Fatalf("nil inputs returned %d outputs, want 0", len(out))
	}

	in := []RankingInput{
		makeRankingInput("asset-X", "channel-X", "video-X"),
	}
	out := PopulateRepetitionPenalty(in, "")
	if !floatNear(out[0].RepetitionPenalty, 0.0, 1e-9) {
		t.Fatalf("empty prevVideoID RepetitionPenalty = %v, want 0 (±1e-9; first scene)",
			out[0].RepetitionPenalty)
	}
}

// ── Immutable input contract ──────────────────────────────────

// TestRanker_PopulateRepetitionPenalty_ImmutableInput pins
// godlike/06 SSOT (immutable input contract): the function must NOT
// mutate the input slice. A regression here would corrupt the
// caller's candidate pool.
func TestRanker_PopulateRepetitionPenalty_ImmutableInput(t *testing.T) {
	t.Parallel()

	in := []RankingInput{
		makeRankingInput("asset-A", "channel-A", "video-A"),
	}
	if !floatNear(in[0].RepetitionPenalty, 0.0, 1e-9) {
		t.Fatalf("pristine RankingInput must have zero RepetitionPenalty, got %v", in[0].RepetitionPenalty)
	}

	out := PopulateRepetitionPenalty(in, "video-A")

	want := DefaultRepetitionPenaltyWeights().SameVideoInConsecutiveScenePenalty
	if !floatNear(out[0].RepetitionPenalty, want, 1e-9) {
		t.Fatalf("PopulateRepetitionPenalty output must reflect the penalty (~%v ± 1e-9); got %v",
			want, out[0].RepetitionPenalty)
	}
	if !floatNear(in[0].RepetitionPenalty, 0.0, 1e-9) {
		t.Fatalf("PopulateRepetitionPenalty mutated the input slice; original RepetitionPenalty = %v, want 0 (±1e-9)",
			in[0].RepetitionPenalty)
	}
}

// floatNear reports whether a and b sit within tolerance. godlike/06
// SSOT (racy-free math): IEEE 754 addition of canonical constants
// introduces tiny precision drift; a 1e-9 tolerance is the canonical
// "ah, that's the floating-point dust" gate.
func floatNear(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}
