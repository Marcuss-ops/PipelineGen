package scriptgeneration

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	frameHoldVideoSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	frameHoldAudioSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// TestFinalJobCompiledPlanCapsExhaustedStockTailToItsFrames pins the r25-r28
// failure: a 60s stock tail whose frame-aligned master needs one frame more
// than the file contains must ask for 1440 frames, not 1441. The muxer holds
// the last displayed frame for the final <1/24s.
func TestFinalJobCompiledPlanCapsExhaustedStockTailToItsFrames(t *testing.T) {
	scenes := []map[string]any{{
		"scene_id":         "closing-10",
		"duration_seconds": 60.041667,
		"kind":             "stock",
		"stock": map[string]any{
			"asset_id": "base-video", "sha256": frameHoldVideoSHA, "size_bytes": int64(1000),
			"duration_ms": int64(60000), "start_ms": int64(0),
		},
	}}
	audio := FinalAudioReference{DurationUS: 60_041_667, DurationMS: 60042, CopyEligible: true, FinalMix: true, PlanSHA256: strings.Repeat("d", 64)}
	audioRef := map[string]any{"asset_id": "final-audio", "sha256": frameHoldAudioSHA, "size_bytes": int64(200), "duration_ms": int64(60042)}

	raw, _, err := finalJobCompiledPlanPayload(scenes, nil, nil, audio, audioRef, 60_041_667)
	if err != nil {
		t.Fatalf("a 60s stock tail with a frame-aligned 60.041667s master must hold, not fail: %v", err)
	}

	var plan finalJobCompiledPlan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.DurationUS != 60_041_667 {
		t.Fatalf("plan duration = %d, want the certified 60041667us master", plan.DurationUS)
	}
	if len(plan.VideoTracks) != 1 || len(plan.VideoTracks[0].Segments) != 1 {
		t.Fatalf("segments = %+v, want one stock tail", plan.VideoTracks)
	}
	segment := plan.VideoTracks[0].Segments[0]
	if segment.FrameCount != 1440 {
		t.Fatalf("tail frame_count = %d, want 1440 (the source's certified frames, never 1441)", segment.FrameCount)
	}
	if segment.SourceInUS != 0 || segment.SourceDurationUS != 60_000_000 {
		t.Fatalf("tail source window = %d+%d, want the whole 60s file", segment.SourceInUS, segment.SourceDurationUS)
	}
}

// TestFinalJobCompleteVideoFrameCoverageHoldsTrailingStockPoolSlot pins the
// live shape the plain stock test missed: a real stock binding is marked
// StockPoolSlot, so it skipped the certified-frame cap and the completion pass
// re-added the 1441st frame after the 60s file was exhausted. The last stock
// placement must instead stop at 1440 and let the muxer hold the final frame.
func TestFinalJobCompleteVideoFrameCoverageHoldsTrailingStockPoolSlot(t *testing.T) {
	segments := []finalJobPlanSegment{{
		SegmentID: "closing-10", AssetID: "stock", SHA256: frameHoldVideoSHA,
		TimelineStartFrame: 0, FrameCount: 1441, StockPoolSlot: true,
		SourceInUS: 1_000, SourceDurationUS: 59_999_000,
	}}
	assets := []finalJobPlanAsset{{AssetID: "stock", Kind: "video", DurationUS: 60_000_000, FrameCount: 1440}}

	got, err := finalJobCompleteVideoFrameCoverage(segments, assets, 60_041_667, 24, 1)
	if err != nil {
		t.Fatalf("a trailing stock pool slot with a one-frame audio tail must hold, not fail: %v", err)
	}
	if got[0].FrameCount != 1440 {
		t.Fatalf("stock tail frame_count = %d, want 1440 (never the 1441st frame)", got[0].FrameCount)
	}
	if got[0].SourceInUS != 1_000 || got[0].SourceDurationUS != 59_999_000 {
		t.Fatalf("the immutable stock binding changed: %d+%d", got[0].SourceInUS, got[0].SourceDurationUS)
	}
	plan := finalJobCompiledPlan{
		DurationUS:  60_041_667,
		Output:      finalJobPlanOutputBlock(),
		VideoTracks: []finalJobPlanVideoTrack{{TrackID: "main-video", Segments: got}},
	}
	if err := finalJobValidateVideoCoverage(plan); err != nil {
		t.Fatalf("one held frame after the stock tail is the allowed final gap: %v", err)
	}
}

// TestFinalJobValidateVideoCoverageAllowsExactlyOneHoldFrame pins the contract:
// one missing frame is the muxer's freeze tail; two are a real gap.
func TestFinalJobValidateVideoCoverageAllowsExactlyOneHoldFrame(t *testing.T) {
	plan := finalJobCompiledPlan{
		DurationUS: 60_041_667,
		Output:     finalJobPlanOutputBlock(),
		VideoTracks: []finalJobPlanVideoTrack{{TrackID: "main-video", Segments: []finalJobPlanSegment{{
			SegmentID: "segment-0000", AssetID: "base-video", SHA256: frameHoldVideoSHA,
			TimelineStartFrame: 0, FrameCount: 1440, SourceInUS: 0, SourceDurationUS: 60_000_000,
		}}}},
	}
	if err := finalJobValidateVideoCoverage(plan); err != nil {
		t.Fatalf("one missing frame is the final hold, not a gap: %v", err)
	}
	plan.DurationUS = 60_083_334 // 1442 frames
	if err := finalJobValidateVideoCoverage(plan); err == nil {
		t.Fatal("a two-frame gap must stay fail-closed")
	}
}

// TestFinalJobValidateVideoCoverageRejectsAnOverrun proves a track longer than
// the audio master is still rejected.
func TestFinalJobValidateVideoCoverageRejectsAnOverrun(t *testing.T) {
	plan := finalJobCompiledPlan{
		DurationUS: 60_000_000,
		Output:     finalJobPlanOutputBlock(),
		VideoTracks: []finalJobPlanVideoTrack{{TrackID: "main-video", Segments: []finalJobPlanSegment{{
			SegmentID: "segment-0000", AssetID: "base-video", SHA256: frameHoldVideoSHA,
			TimelineStartFrame: 0, FrameCount: 1441,
		}}}},
	}
	if err := finalJobValidateVideoCoverage(plan); err == nil {
		t.Fatal("video longer than the audio timeline must stay fail-closed")
	}
}
