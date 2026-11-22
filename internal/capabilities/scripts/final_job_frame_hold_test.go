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

// TestFinalJobCompiledPlanRejectsUnabsorbableStockTail pins the remote
// constraint discovered by E2E-04: a 60s stock tail whose frame-aligned master
// needs one frame more than the file contains cannot be sent as a 1440-frame
// timeline, because the Master validates the timeline against the plan duration
// and rejects the one-frame hold. With no earlier stock-pool placement to
// absorb it, the producer must fail closed instead of emitting a plan the
// remote will refuse.
func TestFinalJobCompiledPlanRejectsUnabsorbableStockTail(t *testing.T) {
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
	if err == nil {
		t.Fatalf("an unabsorbable one-frame stock tail must fail closed, got plan %s", raw)
	}
	if !strings.Contains(err.Error(), "no earlier stock-pool placement can absorb it") {
		t.Fatalf("unexpected error: %v", err)
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

// TestFinalJobCompleteVideoFrameCoverageCertifiesTrailingPreparedHold pins the
// r26b clip_only shape: media_mode=clip_only with stock_enabled=disabled leaves
// the plan with no StockPoolSlot to absorb the final AAC frame, but the trailing
// clip is a Producer-prepared cut, so the plan can carry that frame itself. The
// source window stays inside the asset and the local preparer clones its final
// frame, so the timeline covers the audio master exactly and stays admissible.
func TestFinalJobCompleteVideoFrameCoverageCertifiesTrailingPreparedHold(t *testing.T) {
	segments := []finalJobPlanSegment{{
		SegmentID: "segment-0000", AssetID: "clip", SHA256: frameHoldVideoSHA,
		TimelineStartFrame: 0, FrameCount: 50, SourceInUS: 0, SourceDurationUS: 2_083_333,
	}}
	assets := []finalJobPlanAsset{{AssetID: "clip", SHA256: frameHoldVideoSHA, Kind: "video", DurationUS: 2_083_333, FrameCount: 50}}

	// 51 frames at 24 fps: the AAC master is 1/24s longer than the 50-frame clip.
	got, err := finalJobCompleteVideoFrameCoverage(segments, assets, finalJobFrameStartUS(51, 24, 1), 24, 1)
	if err != nil {
		t.Fatalf("a prepared trailing clip must certify the one-frame tail: %v", err)
	}
	if got[0].FrameCount != 51 {
		t.Fatalf("trailing clip frame_count = %d, want 51 (the certified hold frame)", got[0].FrameCount)
	}
	if got[0].SourceInUS != 0 || got[0].SourceDurationUS != 2_083_333 {
		t.Fatalf("certifying the hold rewrote the source window: %d+%d", got[0].SourceInUS, got[0].SourceDurationUS)
	}
	plan := finalJobCompiledPlan{
		DurationUS:  finalJobFrameStartUS(51, 24, 1),
		Output:      finalJobPlanOutputBlock(),
		VideoTracks: []finalJobPlanVideoTrack{{TrackID: "main-video", Segments: got}},
		Assets:      assets,
	}
	if err := finalJobValidateVideoCoverage(plan); err != nil {
		t.Fatalf("the held frame must close the timeline exactly: %v", err)
	}
	// The pipeline realigns and revalidates right after coverage: the hold must
	// survive both without asking the source for a frame it does not have.
	if err := alignFinalJobSegmentSourceWindows(&plan); err != nil {
		t.Fatalf("align after the certified hold: %v", err)
	}
	if plan.VideoTracks[0].Segments[0].FrameCount != 51 {
		t.Fatalf("alignment dropped the hold frame: %+v", plan.VideoTracks[0].Segments[0])
	}
	if err := validateFinalJobPlanBindingRules(plan); err != nil {
		t.Fatalf("certified hold plan violates the binding rules: %v", err)
	}
}

// TestFinalJobCompleteVideoFrameCoverageRejectsTwoFramePreparedTail keeps the
// fail-closed boundary: a prepared clip can certify one rounding frame, never a
// real timeline/audio mismatch.
func TestFinalJobCompleteVideoFrameCoverageRejectsTwoFramePreparedTail(t *testing.T) {
	segments := []finalJobPlanSegment{{
		SegmentID: "segment-0000", AssetID: "clip", SHA256: frameHoldVideoSHA,
		TimelineStartFrame: 0, FrameCount: 50, SourceInUS: 0, SourceDurationUS: 2_083_333,
	}}
	assets := []finalJobPlanAsset{{AssetID: "clip", SHA256: frameHoldVideoSHA, Kind: "video", DurationUS: 2_083_333, FrameCount: 50}}

	_, err := finalJobCompleteVideoFrameCoverage(segments, assets, finalJobFrameStartUS(52, 24, 1), 24, 1)
	if err == nil || !strings.Contains(err.Error(), "needs 2 more frames") {
		t.Fatalf("a two-frame prepared tail must stay fail-closed, got %v", err)
	}
}

// TestFinalJobCompiledPlanCertifiesClipOnlyTail is the end-to-end pin for the
// r26b live failure: one clip_only scene, stock disabled, and an audio master
// 7ms longer than the clip. The compile pass used to die with "no earlier
// stock-pool placement can absorb it" at PUBLISHING_DOCUMENTS; it must now seal
// a plan whose timeline covers the certified audio exactly.
func TestFinalJobCompiledPlanCertifiesClipOnlyTail(t *testing.T) {
	scenes := []map[string]any{{
		"duration_seconds": 2.083,
		"kind":             "clip",
		"text":             "Una democrazia fragile",
		"stock": map[string]any{
			"asset_id": "clip-1", "sha256": frameHoldVideoSHA, "size_bytes": int64(1000),
			"duration_ms": int64(2083), "start_ms": int64(0),
		},
	}}
	audio := FinalAudioReference{
		DurationUS: 2_090_000, DurationMS: 2090, CopyEligible: true, FinalMix: true,
		PlanSHA256: strings.Repeat("d", 64),
	}
	audioRef := map[string]any{"asset_id": "final-audio", "sha256": frameHoldAudioSHA, "size_bytes": int64(200), "duration_ms": int64(2090)}

	raw, _, err := finalJobCompiledPlanPayload(scenes, nil, nil, audio, audioRef, 2_083_000)
	if err != nil {
		t.Fatalf("a clip-only plan with a one-frame AAC tail must compile: %v", err)
	}
	var plan finalJobCompiledPlan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.VideoTracks) != 1 || len(plan.VideoTracks[0].Segments) != 1 {
		t.Fatalf("segments = %+v, want the single clip placement", plan.VideoTracks)
	}
	segment := plan.VideoTracks[0].Segments[0]
	if segment.FrameCount != 51 || segment.TimelineStartFrame != 0 {
		t.Fatalf("clip placement = %d frames at %d, want 51 at 0 (the final AAC frame certified)", segment.FrameCount, segment.TimelineStartFrame)
	}
	if end := segment.SourceInUS + segment.SourceDurationUS; end != 2_083_000 {
		t.Fatalf("clip source window ends at %d, want the certified 2083000", end)
	}
	if plan.DurationUS != 2_090_000 {
		t.Fatalf("plan duration = %d, want the certified audio duration 2090000", plan.DurationUS)
	}
	if err := finalJobValidateVideoCoverage(plan); err != nil {
		t.Fatalf("the clip-only plan must cover the audio master exactly: %v", err)
	}
}

// TestFinalJobCompleteVideoFrameCoverageAbsorbsTailInEarlierStockSlot pins the
// remote shape: a fixed trailing outro cannot donate a frame past its certified
// render, so the one-frame audio tail must come from the earlier stock-pool
// placement, which the Master loops. The trailing placement shifts by one frame
// and the track stays contiguous and exact.
func TestFinalJobCompleteVideoFrameCoverageAbsorbsTailInEarlierStockSlot(t *testing.T) {
	segments := []finalJobPlanSegment{
		{
			SegmentID: "body-stock", AssetID: "stock", SHA256: frameHoldVideoSHA,
			TimelineStartFrame: 0, FrameCount: 100, StockPoolSlot: true,
		},
		{
			SegmentID: "outro", AssetID: "outro", SHA256: frameHoldVideoSHA,
			TimelineStartFrame: 100, FrameCount: 50, SourceInUS: 0, SourceDurationUS: 2_083_333,
		},
	}
	assets := []finalJobPlanAsset{{AssetID: "outro", Kind: "video", DurationUS: 2_083_333, FrameCount: 50}}

	got, err := finalJobCompleteVideoFrameCoverage(segments, assets, 6_291_667, 24, 1)
	if err != nil {
		t.Fatalf("the earlier stock slot must absorb the one-frame tail: %v", err)
	}
	if len(got) != 2 || got[0].FrameCount != 101 || got[1].TimelineStartFrame != 101 || got[1].FrameCount != 50 {
		t.Fatalf("tail absorption = %+v, want stock 101 then outro at 101 with 50 frames", got)
	}
	plan := finalJobCompiledPlan{
		DurationUS:  6_291_667,
		Output:      finalJobPlanOutputBlock(),
		VideoTracks: []finalJobPlanVideoTrack{{TrackID: "main-video", Segments: got}},
	}
	if err := finalJobValidateVideoCoverage(plan); err != nil {
		t.Fatalf("the absorbed tail must cover the audio master exactly: %v", err)
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
