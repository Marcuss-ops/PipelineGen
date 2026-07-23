// Package mediamemory — ranker_recency_test.go is the Fase 2.2
// unit-test surface for the channel-recency penalty component
// (24h window) and the Top-10 rose deterministic-but-diversified
// selection helper (PickTopFromRose).
//
// godlike/06 SSOT (formula pin): the channel-recency constants
// (ChannelRecencyWindowMaxAge, ChannelRecencyPenaltyPerEvent,
// ChannelRecencyMaxPenalty, RepetitionPenaltyTotalCeiling) are
// baked into the canonical Fase 2.2 formula. Any drift here is
// a ranker-side regression.
//
// godlike/06 SSOT (diversity pin): DiversityFinalScoreDelta is
// the canonical ceiling for the rosa diversity swap. A future
// tuning knob lands via RepetitionPenaltyWeightsV2, not by
// mutating this constant.
//
// godlike/07 NO-FAKE-AVAILABILITY: every test injects a fixed
// `now` so the 24h-window math is racy-free. Tests that don't
// exercise the recency component explicitly pass time.Time{}
// (zero-value) so the existing 3-component formula stays
// deterministic.
package mediamemory

import (
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	"github.com/google/uuid"
)

// ── TestRanker_ThreeVideosSameChannel_MeetsThreshold ────────────
//
// SPEC requirement (Fase 2.2):
// "Aggiungi unit test che verifica che la stessa clip in 3 video
// dello stesso canale → penalty ≥ 0.50".
//
// Same clip (asset-A) seen in 3 different videos (video-1, video-2,
// video-3) on the same channel (channel-X), all within the last
// 24h. Verify the total RepetitionPenalty ≥ 0.50.
//
// Trace under the canonical Fase 2.2 formula:
//  1. SameAssetPenalty: 3 sightings → cap 1.0
//  2. SameVideoInConsecutiveScenePenalty: not fired (candidate
//     carries video-3, prevVideoID="" — first scene of test)
//  3. ChannelSaturationPenalty: 3 sightings, MinSightings=3 →
//     0.1 * (3-3) = 0.0
//  4. ChannelRecencyPenalty: 3 in-window events → 0.20 * 3 =
//     0.60, capped at ChannelRecencyMaxPenalty=0.50
//     TOTAL: 1.0 + 0.0 + 0.0 + 0.50 = 1.50 (ceiling clamp hits)
//     ≥ 0.50 ✓
//
// godlike/06 SSOT (deterministic now): now is fixed so the
// 24h-window math is reproducible across runs.
func TestRanker_ThreeVideosSameChannel_MeetsThreshold(t *testing.T) {
	t.Parallel()

	const (
		projectID   = "proj-maya-doc"
		clipAssetID = "asset-maya-temple-clip"
		channelID   = "channel-maya-history"
		video1      = "video-maya-1"
		video2      = "video-maya-2"
		video3      = "video-maya-3"
	)

	// `now` is the canonical reference point. All history events
	// fall within the 24h window relative to `now`.
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	mkEvent := func(at time.Time, videoID string) UsageEvent {
		return UsageEvent{
			ID:        uuid.NewString(),
			ProjectID: projectID,
			AssetID:   clipAssetID,
			ChannelID: channelID,
			VideoID:   videoID,
			SlotKind:  media.SlotPrimaryVideo,
			CreatedAt: at,
		}
	}

	history := []UsageEvent{
		mkEvent(now.Add(-23*time.Hour), video1), // within 24h
		mkEvent(now.Add(-12*time.Hour), video2), // within 24h
		mkEvent(now.Add(-1*time.Hour), video3),  // within 24h
	}

	// Candidate: same clip, same channel, different scene's video.
	in := []RankingInput{
		{
			Candidate: MediaCandidate{
				AssetID:   clipAssetID,
				ChannelID: channelID,
				VideoID:   video3,
			},
		},
	}

	out := PopulateRepetitionPenalty(in, history, "", now)

	if len(out) != 1 {
		t.Fatalf("expected 1 output, got %d", len(out))
	}
	got := out[0].RepetitionPenalty
	if got < 0.50 {
		t.Fatalf("same clip in 3 videos same channel → RepetitionPenalty = %v, want >= 0.50 "+
			"(trace: 1.0 SameAsset + 0.0 ChannelSaturation (3 events, threshold 3) + 0.50 ChannelRecency cap)",
			got)
	}
	// Sanity: the ceiling clamp at 1.5 caps the exact total.
	if got > RepetitionPenaltyTotalCeiling {
		t.Fatalf("RepetitionPenalty = %v exceeds canonical ceiling %v",
			got, RepetitionPenaltyTotalCeiling)
	}
}

// ── TestRanker_PopulateChannelRecencyPenalty_FreshWindow ────────
//
// All project history events for the candidate's ChannelID are
// within the 24h window. Each contributes ChannelRecencyPenaltyPerEvent
// before the ceiling clamp.
func TestRanker_PopulateChannelRecencyPenalty_FreshWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

	history := []UsageEvent{
		{ProjectID: "p", AssetID: "asset-X", ChannelID: "channel-X",
			VideoID: "v1", SlotKind: media.SlotPrimaryVideo,
			CreatedAt: now.Add(-1 * time.Hour)},
		{ProjectID: "p", AssetID: "asset-X", ChannelID: "channel-X",
			VideoID: "v2", SlotKind: media.SlotPrimaryVideo,
			CreatedAt: now.Add(-2 * time.Hour)},
	}

	// Asset+channel-only candidate (asset-X NOT in assetSightings
	// map because AssetID != "asset-X"; we use a different asset
	// to isolate the recency component).
	in := []RankingInput{
		{Candidate: MediaCandidate{
			AssetID:   "asset-other",
			ChannelID: "channel-X",
			VideoID:   "v9",
		}},
	}

	out := PopulateRepetitionPenalty(in, history, "", now)
	// Expected: 0 SameAsset (asset-other not seen) + 0 Consecutive
	// + 0 ChannelSaturation (2 events, threshold 3) +
	// ChannelRecency 0.20 * 2 = 0.40.
	want := 0.40
	if !floatNear(out[0].RepetitionPenalty, want, 1e-9) {
		t.Fatalf("fresh-window recency penalty = %v, want %v (±1e-9)",
			out[0].RepetitionPenalty, want)
	}
}

// ── TestRanker_PopulateChannelRecencyPenalty_ExpiredWindow ─────
//
// All project history events for the candidate's ChannelID are
// older than 24h. The recency component stays 0.
func TestRanker_PopulateChannelRecencyPenalty_ExpiredWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

	history := []UsageEvent{
		{ProjectID: "p", AssetID: "asset-X", ChannelID: "channel-X",
			VideoID: "v1", SlotKind: media.SlotPrimaryVideo,
			CreatedAt: now.Add(-48 * time.Hour)}, // 2 days ago → expired
		{ProjectID: "p", AssetID: "asset-X", ChannelID: "channel-X",
			VideoID: "v2", SlotKind: media.SlotPrimaryVideo,
			CreatedAt: now.Add(-72 * time.Hour)}, // 3 days ago → expired
	}

	in := []RankingInput{
		{Candidate: MediaCandidate{
			AssetID:   "asset-other",
			ChannelID: "channel-X",
			VideoID:   "v9",
		}},
	}

	out := PopulateRepetitionPenalty(in, history, "", now)
	// Expected: 0 + 0 + 0 + 0 = 0.0 (expired events contribute nothing).
	if !floatNear(out[0].RepetitionPenalty, 0.0, 1e-9) {
		t.Fatalf("expired-window recency penalty = %v, want 0.0 (±1e-9; events > 24h old contribute nothing)",
			out[0].RepetitionPenalty)
	}
}

// ── TestRanker_PopulateChannelRecencyPenalty_CapsAtMax ──────────
//
// Many in-window events accumulate beyond ChannelRecencyMaxPenalty.
// The per-candidate ceiling clamp holds the value at
// ChannelRecencyMaxPenalty.
func TestRanker_PopulateChannelRecencyPenalty_CapsAtMax(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

	// 10 events on the same channel within the 24h window.
	// Without the recency cap: 0.20 * 10 = 2.0. With the cap: 0.50.
	history := make([]UsageEvent, 0, 10)
	for i := 0; i < 10; i++ {
		history = append(history, UsageEvent{
			ProjectID: "p", AssetID: "asset-Z",
			ChannelID: "channel-X",
			VideoID:   "v" + uuid.NewString()[:4],
			SlotKind:  media.SlotPrimaryVideo,
			CreatedAt: now.Add(time.Duration(-i) * time.Hour),
		})
	}

	// Candidate carries a DIFFERENT AssetID so SameAssetPenalty
	// stays 0 (isolates the channel-saturation + recency math).
	in := []RankingInput{
		{Candidate: MediaCandidate{
			AssetID:   "asset-other",
			ChannelID: "channel-X",
			VideoID:   "v9",
		}},
	}

	out := PopulateRepetitionPenalty(in, history, "", now)
	// Trace:
	//   SameAsset          = 0 (asset-other not in history)
	//   Consecutive        = 0 (prevVideoID="")
	//   ChannelSaturation  = 0.1 * (10-3) = 0.7 (10 events, threshold 3)
	//   ChannelRecency     = min(0.20*10, 0.50) = 0.50 (cap fired)
	//   Total              = 0 + 0 + 0.7 + 0.50 = 1.20
	// The cap on the recency component is the load-bearing
	// assertion: without it, the value would be 2.20 and the
	// test would fail. RepetitionPenaltyTotalCeiling (1.5) is
	// also respected (1.20 ≤ 1.5).
	want := 1.20
	if !floatNear(out[0].RepetitionPenalty, want, 1e-9) {
		t.Fatalf("recency-cap penalty total = %v, want %v (±1e-9; 0.7 ChannelSaturation + 0.50 ChannelRecency cap)",
			out[0].RepetitionPenalty, want)
	}
	if out[0].RepetitionPenalty > RepetitionPenaltyTotalCeiling+1e-9 {
		t.Fatalf("recency-cap penalty %v exceeds RepetitionPenaltyTotalCeiling %v",
			out[0].RepetitionPenalty, RepetitionPenaltyTotalCeiling)
	}
}

// ── TestRanker_PopulateChannelRecencyPenalty_ZeroNow_Disables ──
//
// When `now` is the zero value, the recency component must NOT
// fire (godlike/07 backward compat for callers that haven't been
// updated to inject a real timestamp yet).
func TestRanker_PopulateChannelRecencyPenalty_ZeroNow_Disables(t *testing.T) {
	t.Parallel()

	// All events set to "now-ish" so a real `now` would fire recency.
	t0 := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	history := []UsageEvent{
		{ProjectID: "p", AssetID: "asset-X", ChannelID: "channel-X",
			VideoID: "v1", SlotKind: media.SlotPrimaryVideo, CreatedAt: t0},
		{ProjectID: "p", AssetID: "asset-X", ChannelID: "channel-X",
			VideoID: "v2", SlotKind: media.SlotPrimaryVideo, CreatedAt: t0},
	}

	in := []RankingInput{
		{Candidate: MediaCandidate{
			AssetID:   "asset-other",
			ChannelID: "channel-X",
			VideoID:   "v9",
		}},
	}

	out := PopulateRepetitionPenalty(in, history, "", time.Time{})
	// With `now` = zero, recency component is disabled → only
	// SameAsset (0) + Consecutive (0) + ChannelSaturation (0; 2 events,
	// threshold 3) = 0.
	if !floatNear(out[0].RepetitionPenalty, 0.0, 1e-9) {
		t.Fatalf("zero now must disable recency penalty; got %v, want 0",
			out[0].RepetitionPenalty)
	}
}

// ── TestRanker_PickTopFromRose_DeterministicByScore ─────────────
//
// The rosa is sorted by FinalScore DESC (with AssetID ASC tiebreak
// via sort.SliceStable + lessRanked). PickTopFromRose returns
// rose[0] when no prevVideoID is supplied (no diversity pressure).
func TestRanker_PickTopFromRose_DeterministicByScore(t *testing.T) {
	t.Parallel()

	// godlike/06 SSOT: PickTopFromRose expects the caller to
	// supply a SORTED rose (FinalScore DESC, AssetID ASC tiebreak).
	// The helper does NOT sort internally — sorting is the caller's
	// job (godlike/06 single-responsibility: sortByFinalScoreDesc
	// is the canonical sort seam in resolver.go).
	rose := []rankedCandidate{
		// Sorted DESC by FinalScore: asset-A (0.9), asset-B (0.7), asset-C (0.5).
		{fc: FilteredCandidate{Candidate: MediaCandidate{AssetID: "asset-A"}},
			out: RankingOutput{FinalScore: 0.9}},
		{fc: FilteredCandidate{Candidate: MediaCandidate{AssetID: "asset-B"}},
			out: RankingOutput{FinalScore: 0.7}},
		{fc: FilteredCandidate{Candidate: MediaCandidate{AssetID: "asset-C"}},
			out: RankingOutput{FinalScore: 0.5}},
	}

	got := PickTopFromRose(rose, "")
	if got.fc.Candidate.AssetID != "asset-A" {
		t.Fatalf("PickTopFromRose empty prev = %q, want asset-A (highest score in sorted rose)",
			got.fc.Candidate.AssetID)
	}
}

// ── TestRanker_PickTopFromRose_DiversitySwap ────────────────────
//
// top-1 shares VideoID with prevVideoID. An alternative within
// DiversityFinalScoreDelta (0.10) exists → the helper swaps to
// the alternative for diversity.
func TestRanker_PickTopFromRose_DiversitySwap(t *testing.T) {
	t.Parallel()

	rose := []rankedCandidate{
		// top-1: video "shared-vid", score 0.80
		{fc: FilteredCandidate{Candidate: MediaCandidate{AssetID: "asset-A", VideoID: "shared-vid"}},
			out: RankingOutput{FinalScore: 0.80}},
		// alt 1: video "alt-vid", score 0.75 (within delta=0.10) → SWAP candidate
		{fc: FilteredCandidate{Candidate: MediaCandidate{AssetID: "asset-B", VideoID: "alt-vid"}},
			out: RankingOutput{FinalScore: 0.75}},
		// alt 2: video "other-vid", score 0.40 (outside delta)
		{fc: FilteredCandidate{Candidate: MediaCandidate{AssetID: "asset-C", VideoID: "other-vid"}},
			out: RankingOutput{FinalScore: 0.40}},
	}

	got := PickTopFromRose(rose, "shared-vid")
	if got.fc.Candidate.AssetID != "asset-B" {
		t.Fatalf("PickTopFromRose diversity swap = %q, want asset-B (alt within delta=0.10)",
			got.fc.Candidate.AssetID)
	}
}

// ── TestRanker_PickTopFromRose_NoDiverseAlternative ─────────────
//
// top-1 shares VideoID with prevVideoID but ALL alternatives
// either also share the same video OR fall outside the delta
// ceiling. The helper returns top-1 (no swap possible).
func TestRanker_PickTopFromRose_NoDiverseAlternative(t *testing.T) {
	t.Parallel()

	rose := []rankedCandidate{
		// top-1: video "shared-vid", score 0.80
		{fc: FilteredCandidate{Candidate: MediaCandidate{AssetID: "asset-A", VideoID: "shared-vid"}},
			out: RankingOutput{FinalScore: 0.80}},
		// alt 1: same video "shared-vid" → not a valid alternative
		{fc: FilteredCandidate{Candidate: MediaCandidate{AssetID: "asset-B", VideoID: "shared-vid"}},
			out: RankingOutput{FinalScore: 0.78}},
		// alt 2: different video, but score 0.40 → outside delta
		{fc: FilteredCandidate{Candidate: MediaCandidate{AssetID: "asset-C", VideoID: "alt-vid"}},
			out: RankingOutput{FinalScore: 0.40}},
	}

	got := PickTopFromRose(rose, "shared-vid")
	if got.fc.Candidate.AssetID != "asset-A" {
		t.Fatalf("PickTopFromRose no-diverse-alt = %q, want asset-A (top-1 retained when no valid alt)",
			got.fc.Candidate.AssetID)
	}
}

// ── TestRanker_PickTopFromRose_EmptyRose ────────────────────────
//
// godlike/07 NO-FAKE-AVAILABILITY: an empty rose returns the
// zero-value rankedCandidate without panicking.
func TestRanker_PickTopFromRose_EmptyRose(t *testing.T) {
	t.Parallel()

	got := PickTopFromRose(nil, "shared-vid")
	if got.fc.Candidate.AssetID != "" || got.out.FinalScore != 0 {
		t.Fatalf("PickTopFromRose(nil) = %+v, want zero-value rankedCandidate", got)
	}
}
