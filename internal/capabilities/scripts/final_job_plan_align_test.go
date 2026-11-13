package scriptgeneration

import (
	"encoding/json"
	"strings"
	"testing"

	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// TestAlignFinalJobCompiledPlanRejectsPartialStockWindow pins the r56 closure:
// alignment must not preserve a 5.000s plan window against 4.979s saved stock
// metadata. The Master only packet-copies whole files, so the Producer rejects
// the plan before submit.
func TestAlignFinalJobCompiledPlanRejectsPartialStockWindow(t *testing.T) {
	sha := strings.Repeat("d", 64)
	timelineSHA := strings.Repeat("b", 64)
	plan := finalJobCompiledPlan{
		PlanVersion: 2, TimelineRevision: 1, TimelineSHA256: timelineSHA, DurationUS: 5_000_000,
		VideoTracks: []finalJobPlanVideoTrack{{TrackID: "main-video", Segments: []finalJobPlanSegment{
			{SegmentID: "segment-0002", AssetID: "stock-old", SHA256: sha, TimelineStartFrame: 0, FrameCount: 120, SourceInUS: 0, SourceDurationUS: 5_000_000},
		}}},
		Assets: []finalJobPlanAsset{{
			AssetID: "stock-old", SHA256: sha, SizeBytes: 2048, Kind: "video", DurationUS: 5_000_000,
		}},
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	_, err = alignFinalJobCompiledPlanAssetIDs(string(raw), []any{map[string]any{
		"asset_id": sha, "sha256": sha, "size_bytes": int64(2048),
		"kind": "video", "duration_ms": int64(4979), "role": "stock",
	}})
	if err == nil || !strings.Contains(err.Error(), "exceeds saved file duration") {
		t.Fatalf("alignFinalJobCompiledPlanAssetIDs error = %v, want a stock-window out-of-bounds rejection", err)
	}
}

// TestAlignFinalJobCompiledPlanAcceptsWholeStockFileAtItsSavedDuration pins
// the green side: a 4.979s whole file keeps exactly 4.979s/119 frames; it is
// never padded to the 5.000s delivery boundary.
func TestAlignFinalJobCompiledPlanAcceptsWholeStockFileAtItsSavedDuration(t *testing.T) {
	sha := strings.Repeat("d", 64)
	timelineSHA := strings.Repeat("b", 64)
	plan := finalJobCompiledPlan{
		PlanVersion: 2, TimelineRevision: 1, TimelineSHA256: timelineSHA, DurationUS: 4_979_000,
		VideoTracks: []finalJobPlanVideoTrack{{TrackID: "main-video", Segments: []finalJobPlanSegment{
			{SegmentID: "segment-0002", AssetID: "stock-old", SHA256: sha, TimelineStartFrame: 0, FrameCount: 119, SourceInUS: 0, SourceDurationUS: 4_979_000},
		}}},
		Assets: []finalJobPlanAsset{{
			AssetID: "stock-old", SHA256: sha, SizeBytes: 2048, Kind: "video", DurationUS: 4_979_000,
		}},
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	aligned, err := alignFinalJobCompiledPlanAssetIDs(string(raw), []any{map[string]any{
		"asset_id": sha, "sha256": sha, "size_bytes": int64(2048),
		"kind": "video", "duration_ms": int64(4979), "role": "stock",
	}})
	if err != nil {
		t.Fatalf("alignFinalJobCompiledPlanAssetIDs: %v", err)
	}
	var got finalJobCompiledPlan
	if err := json.Unmarshal([]byte(aligned), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Assets) != 1 || got.Assets[0].DurationUS != 4_979_000 {
		t.Fatalf("source extent = %+v, want the exact 4.979s saved duration", got.Assets)
	}
	segment := got.VideoTracks[0].Segments[0]
	if segment.SourceInUS != 0 || segment.SourceDurationUS != 4_979_000 || segment.FrameCount != 119 {
		t.Fatalf("stock window = %d + %d over %d frames, want 0 + 4979000 over 119", segment.SourceInUS, segment.SourceDurationUS, segment.FrameCount)
	}
}

// TestAlignFinalJobSegmentSourceWindowsHoldsOneFrameAtWholeFileTail pins the
// case where source metadata carries 119 frames while the trailing segment
// asks for 120. The whole-file source window is clamped to the declared
// extent, but the output frame count stays 120: the worker holds the last
// displayed frame instead of the plan inventing a sub-window stock segment.
func TestAlignFinalJobSegmentSourceWindowsHoldsOneFrameAtWholeFileTail(t *testing.T) {
	plan := finalJobCompiledPlan{
		Assets: []finalJobPlanAsset{{
			AssetID: "stock-4990", SHA256: strings.Repeat("d", 64), SizeBytes: 2048,
			Kind: "video", DurationUS: 4_990_000,
		}},
		VideoTracks: []finalJobPlanVideoTrack{{TrackID: "main-video", Segments: []finalJobPlanSegment{{
			SegmentID: "segment-0016", AssetID: "stock-4990", SHA256: strings.Repeat("d", 64),
			TimelineStartFrame: 1614, FrameCount: 120, SourceInUS: 0, SourceDurationUS: 5_000_000,
		}}}},
	}
	if err := alignFinalJobSegmentSourceWindows(&plan); err != nil {
		t.Fatalf("alignFinalJobSegmentSourceWindows: %v", err)
	}
	segment := plan.VideoTracks[0].Segments[0]
	if segment.SourceInUS != 0 || segment.SourceDurationUS != 4_990_000 || segment.FrameCount != 120 {
		t.Fatalf("aligned window = %d + %d over %d frames, want the whole file 0 + 4990000 over 120 with the last frame held", segment.SourceInUS, segment.SourceDurationUS, segment.FrameCount)
	}
}

func TestAlignFinalJobCompiledPlanAssetIDsUsesRuntimeIdentity(t *testing.T) {
	sha := strings.Repeat("a", 64)
	timelineSHA := strings.Repeat("b", 64)
	plan := finalJobCompiledPlan{
		PlanVersion: 2, TimelineRevision: 1, TimelineSHA256: timelineSHA,
		FinalAudio: finalJobPlanAudio{AssetID: "audio-old", SHA256: strings.Repeat("c", 64), SizeBytes: 10},
		VideoTracks: []finalJobPlanVideoTrack{{TrackID: "main-video", Segments: []finalJobPlanSegment{{
			SegmentID: "segment-0000", AssetID: "final-fragment-old", SHA256: sha,
			TimelineStartFrame: 12, FrameCount: 48, SourceInUS: 500, SourceDurationUS: 2_000_000,
		}}}},
		Assets: []finalJobPlanAsset{{
			AssetID: "final-fragment-old", SHA256: sha, SizeBytes: 10,
			Kind: "video", DurationUS: 2_000_000, FrameCount: 48,
		}},
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	aligned, err := alignFinalJobCompiledPlanAssetIDs(string(raw), []any{map[string]any{
		"asset_id": sha, "sha256": sha, "size_bytes": int64(10),
		"kind": "video", "mime": "video/mp4", "duration_us": int64(2_000_000),
		"width": 1920, "height": 1080, "profile_id": kernelmedia.OverlayVideoOnlyProfileID,
		"frame_count": int64(48), "timeline_revision": int64(1), "timeline_sha256": timelineSHA,
		"timeline_start_frame": int64(12), "first_frame_keyframe": true, "closed_gop": true,
	}})
	if err != nil {
		t.Fatalf("alignFinalJobCompiledPlanAssetIDs: %v", err)
	}
	var got finalJobCompiledPlan
	if err := json.Unmarshal([]byte(aligned), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Assets) != 1 || got.Assets[0].AssetID != sha || got.Assets[0].AssetKey != sha || got.Assets[0].ProfileID != kernelmedia.AssemblyMediaContractID || got.Assets[0].TimelineStartFrame != 12 {
		t.Fatalf("assets = %+v, want canonical SHA asset/key with V1 profile and segment timeline start", got.Assets)
	}
	segment := got.VideoTracks[0].Segments[0]
	if segment.AssetID != sha || segment.SHA256 != sha || segment.SourceInUS != 500 || segment.SourceDurationUS != 2_000_000 || segment.TimelineStartFrame != 12 {
		t.Fatalf("segment = %+v, want SHA id, preserved source offset and exact runtime metadata", segment)
	}
}
