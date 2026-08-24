// Package mediamemory — ranker_test.go is the Fase 2.3
// anti-repetition unit-test surface for PopulateRepetitionPenalty.
//
// godlike/06 SSOT (formula pin): the three penalty seats
// (SameAssetPenalty, SameVideoInConsecutiveScenePenalty,
// ChannelSaturationBase) are baked into the formula and any
// drift here is a ranker-side regression. These tests assert the
// canonical Phase 2.3 contract so a future tuning knob pins
// here, not at the ranker math layer.
//
// godlike/07 NO-FAKE-AVAILABILITY: the tests use deterministic
// inputs (no RNG, no time.Now, no env-var seams) so the
// formula's monotonic behaviour across repetition is
// racy-free under -race.
//
// godlike/06 SSOT (closed-set assets): the candidates' AssetIDs
// and histories' ChannelID/VideoID are all canonical strings
// chosen deterministically for assertions.
package mediamemory

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"github.com/google/uuid"
)

// ── Fixture helpers ────────────────────────────────────────────

// makeUsageEvent builds a UsageEvent with the canonical
// (project_id, asset_id, channel_id, video_id) pin set. Tests
// use this to seed the project history with deterministic
// identity so the penalty calculations are reproducible.
func makeUsageEvent(projectID, assetID, channelID, videoID string, slot SlotKind) UsageEvent {
	return UsageEvent{
		ID:        uuid.NewString(),
		ProjectID: projectID,
		AssetID:   assetID,
		ChannelID: channelID,
		VideoID:   videoID,
		SlotKind:  slot,
	}
}

// makeRankingInput returns a minimal RankingInput envelope
// with the binding+candidate identity required for the
// penalty calculation. Tests pin scores to 0 so the penalty
// alone drives FinalScore comparisons.
func makeRankingInput(assetID, channelID, videoID string) RankingInput {
	return RankingInput{
		Candidate: MediaCandidate{
			AssetID:   assetID,
			ChannelID: channelID,
			VideoID:   videoID,
		},
	}
}

// ── TestRanker_PopulateRepetitionPenalty_CrossSceneSaturation ──

// TestRanker_PopulateRepetitionPenalty_CrossSceneSaturation pins
// the SPEC contract:
//  1. Same asset reused 3 times in same project -> penalty >= 1.0
//     (SameAssetPenalty canonical cap; ranker suppresses).
//  2. Candidate sharing prevVideoID -> +0.3 consecutive penalty.
//  3. Channel with 4 sightings -> +0.1 channel-saturation penalty.
//  4. Fresh asset (no reuse, no shared prevVideoID, no saturation)
//     -> penalty == 0 (ranker scores normally).
func TestRanker_PopulateRepetitionPenalty_CrossSceneSaturation(t *testing.T) {
	t.Parallel()

	projectID := "proj-maya-saturation"
	const sameAsset = "asset-A"
	const sameChannel = "channel-maya-history"
	const prevVideo = "video-001"

	// Seed: 3 prior usages of sameAsset on same channel (4 with
	// the empty slot upper bound) — this drives both the
	// SameAsset penalty (cap 1.0) AND the channel-saturation
	// (Base 0.1 × (4 - MinSightings=3) = 0.1).
	history := []UsageEvent{
		makeUsageEvent(projectID, sameAsset, sameChannel, prevVideo, media.SlotPrimaryVideo),
		makeUsageEvent(projectID, sameAsset, sameChannel, prevVideo, media.SlotPrimaryVideo),
		makeUsageEvent(projectID, sameAsset, sameChannel, prevVideo, media.SlotPrimaryVideo),
		makeUsageEvent(projectID, sameAsset, sameChannel, prevVideo, media.SlotPrimaryVideo),
		// Also log a SAME-video entry to anchor the consecutive test.
	}

	// Candidate A: same asset, same channel, carries prevVideo
	// as its VideoID. Should fire SameAsset (capped at 1.0) +
	// SameVideoInConsecutiveScene (0.3) + ChannelSaturation (0.1)
	// = 1.0 + 0.3 + 0.1 = 1.4. Capped at the ranker-side penalty
	// math; we assert >= SameAssetPenalty plus both extras.
	in := []RankingInput{
		makeRankingInput(sameAsset, sameChannel, prevVideo),
		// Candidate B: different asset, different channel,
		// different video — fresh, no penalty.
		makeRankingInput("asset-fresh-X", "channel-fresh-X", "video-fresh-X"),
	}

	out := PopulateRepetitionPenalty(in, history, prevVideo, time.Time{})

	if len(out) != 2 {
		t.Fatalf("PopulateRepetitionPenalty returned %d inputs, want 2", len(out))
	}

	a := out[0]
	// SameAsset penalty: capped at 1.0 (4 sightings → min(4.0, 1.0) = 1.0)
	// + Consecutive penalty: 0.3 (candidateVideoID == prevVideoID)
	// + Channel penalty: 0.1 (4 sightings >= 3)
	wantA := 1.0 + 0.3 + 0.1
	if !floatNear(a.RepetitionPenalty, wantA, 1e-9) {
		t.Fatalf("candidate A RepetitionPenalty = %v, want ~%v (±1e-9; 1.0 same-asset + 0.3 consecutive + 0.1 channel-saturation)",
			a.RepetitionPenalty, wantA)
	}

	b := out[1]
	// Fresh asset (asset-fresh-X not in history) and different
	// channel (channel-fresh-X not in history) and different
	// video (video-fresh-X != prevVideo) → penalty == 0.
	if !floatNear(b.RepetitionPenalty, 0.0, 1e-9) {
		t.Fatalf("candidate B (fresh) RepetitionPenalty = %v, want 0 (±1e-9)", b.RepetitionPenalty)
	}
}

// ── TestRanker_PopulateRepetitionPenalty_NilAndEmptySeams ────

// TestRanker_PopulateRepetitionPenalty_NilAndEmptySeams pins the
// godlike/07 NO-FAKE-AVAILABILITY boundary:
//   - nil history → all penalties stay 0 (no penalty input available).
//   - empty prevVideoID → no consecutive penalty fires (first scene).
//   - nil inputs → empty outputs (no panic).
func TestRanker_PopulateRepetitionPenalty_NilAndEmptySeams(t *testing.T) {
	t.Parallel()

	// nil inputs
	if out := PopulateRepetitionPenalty(nil, nil, "", time.Time{}); len(out) != 0 {
		t.Fatalf("nil inputs returned %d outputs, want 0", len(out))
	}

	// nil history with non-nil inputs (even if reuse-eligible)
	in := []RankingInput{
		makeRankingInput("asset-X", "channel-X", "video-X"),
	}
	out := PopulateRepetitionPenalty(in, nil, "", time.Time{})
	if !floatNear(out[0].RepetitionPenalty, 0.0, 1e-9) {
		t.Fatalf("nil history RepetitionPenalty = %v, want 0 (±1e-9; no penalty input available)",
			out[0].RepetitionPenalty)
	}

	// empty prevVideoID disables the consecutive-source penalty
	// even when candidate VideoID is shared.
	history := []UsageEvent{
		makeUsageEvent("p", "asset-X", "channel-X", "video-X", media.SlotPrimaryVideo),
		makeUsageEvent("p", "asset-X", "channel-X", "video-X", media.SlotPrimaryVideo),
		makeUsageEvent("p", "asset-X", "channel-X", "video-X", media.SlotPrimaryVideo),
		makeUsageEvent("p", "asset-X", "channel-X", "video-X", media.SlotPrimaryVideo),
	}
	out = PopulateRepetitionPenalty(in, history, "", time.Time{})
	// SameAsset penalty still fires (4 sightings), but Consecutive penalty
	// does NOT (empty prevVideoID = first scene). Channel-saturation fires
	// (4 sightings >= 3 → 0.1).
	want := 1.0 + 0.0 + 0.1
	if !floatNear(out[0].RepetitionPenalty, want, 1e-9) {
		t.Fatalf("empty prevVideoID RepetitionPenalty = %v, want ~%v (±1e-9; SameAsset 1.0 + Consecutive 0.0 + Channel 0.1)",
			out[0].RepetitionPenalty, want)
	}
}

// ── TestRanker_PopulateRepetitionPenalty_ImmutableInput ───────

// TestRanker_PopulateRepetitionPenalty_ImmutableInput pins
// godlike/06 SSOT (immutable input contract): the function must
// NOT mutate the input slice. A regression here would corrupt
// the caller's candidate pool.
func TestRanker_PopulateRepetitionPenalty_ImmutableInput(t *testing.T) {
	t.Parallel()

	in := []RankingInput{
		makeRankingInput("asset-A", "channel-A", "video-A"),
	}
	// Snapshot original penalty (must be 0 by ranker-default).
	if !floatNear(in[0].RepetitionPenalty, 0.0, 1e-9) {
		t.Fatalf("pristine RankingInput must have zero RepetitionPenalty, got %v", in[0].RepetitionPenalty)
	}

	history := []UsageEvent{
		makeUsageEvent("p", "asset-A", "channel-A", "video-A", media.SlotPrimaryVideo),
		makeUsageEvent("p", "asset-A", "channel-A", "video-A", media.SlotPrimaryVideo),
		makeUsageEvent("p", "asset-A", "channel-A", "video-A", media.SlotPrimaryVideo),
		makeUsageEvent("p", "asset-A", "channel-A", "video-A", media.SlotPrimaryVideo),
	}
	out := PopulateRepetitionPenalty(in, history, "video-A", time.Time{})

	// Output has non-zero penalty.
	if !floatNear(out[0].RepetitionPenalty, 1.4, 1e-9) {
		t.Fatalf("PopulateRepetitionPenalty output must reflect penalties (1.0 + 0.3 + 0.1 = 1.4 ± 1e-9); got %v",
			out[0].RepetitionPenalty)
	}
	// Input still has zero penalty (godlike/06 SSOT: immutable input).
	if !floatNear(in[0].RepetitionPenalty, 0.0, 1e-9) {
		t.Fatalf("PopulateRepetitionPenalty mutated the input slice; original RepetitionPenalty = %v, want 0 (±1e-9)",
			in[0].RepetitionPenalty)
	}
	// Output is a NEW slice (the function returns a brand-new slice).
	if &out[0] == &in[0] {
		t.Fatalf("PopulateRepetitionPenalty returned a pointer into the input (must return a new slice)")
	}
}

// floatNear reports whether a and b sit within tolerance. godlike/06
// SSOT (racy-free math): IEEE 754 addition of canonical constants
// (1.0 + 0.3 + 0.1) introduces tiny precision drift
// (1.4000000000000001 vs 1.4); a 1e-9 tolerance is the canonical
// "ah, that's the floating-point dust" gate.
func floatNear(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

// Compile-time guard: the package-internal PenaltyWeights + helper
// functions are referenced by these tests; safeguard against
// accidental removal during a refactor.
var _ = DefaultRepetitionPenaltyWeights
var _ = AntiRepetitionHistoryLimit
var _ context.Context
