package scriptgeneration

import (
	"encoding/json"
	"strings"
	"testing"

	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// TestAlignFinalJobCompiledPlanAssetIDsKeepsSharedSourceExtent pins the case
// that made the stock path unsafe: one 5s Drive source backs a full window and
// a 13-frame tail. The tail is the LAST segment, so copying its span onto the
// asset would shrink the source to 525ms and reject the 120-frame placement.
// The saved stock metadata rounds the 5s file down to 4.979s; alignment must
// not shrink the compiled source extent below the window the plan needs.
func TestAlignFinalJobCompiledPlanAssetIDsKeepsSharedSourceExtent(t *testing.T) {
	sha := strings.Repeat("d", 64)
	timelineSHA := strings.Repeat("b", 64)
	plan := finalJobCompiledPlan{
		PlanVersion: 2, TimelineRevision: 1, TimelineSHA256: timelineSHA, DurationUS: 5_000_000,
		VideoTracks: []finalJobPlanVideoTrack{{TrackID: "main-video", Segments: []finalJobPlanSegment{
			{SegmentID: "segment-0002", AssetID: "stock-old", SHA256: sha, TimelineStartFrame: 0, FrameCount: 120, SourceInUS: 0, SourceDurationUS: 5_000_000},
			{SegmentID: "segment-0003", AssetID: "stock-old", SHA256: sha, TimelineStartFrame: 120, FrameCount: 13, SourceInUS: 4_475_000, SourceDurationUS: 525_000},
		}}},
		Assets: []finalJobPlanAsset{{
			AssetID: "stock-old", SHA256: sha, SizeBytes: 2048, Kind: "video", DurationUS: 5_000_000,
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
	if len(got.Assets) != 1 || got.Assets[0].DurationUS != 5_000_000 || got.Assets[0].FrameCount != 0 {
		t.Fatalf("source extent = %+v, want the full 5s source with no placement-local frame count", got.Assets)
	}
	if err := alignFinalJobSegmentSourceWindows(&got); err != nil {
		t.Fatalf("alignFinalJobSegmentSourceWindows after shared-source align: %v", err)
	}
	full, tail := got.VideoTracks[0].Segments[0], got.VideoTracks[0].Segments[1]
	if full.SourceInUS != 0 || full.SourceDurationUS != 5_000_000 {
		t.Fatalf("full window = %d + %d, want 0 + 5000000", full.SourceInUS, full.SourceDurationUS)
	}
	if tail.SourceInUS != 4_458_333 || tail.SourceDurationUS != 541_667 {
		t.Fatalf("tail window = %d + %d, want 4458333 + 541667", tail.SourceInUS, tail.SourceDurationUS)
	}
}

// TestAlignFinalJobSegmentSourceWindowsAllowsOneFrameDurationRounding pins the
// r49 case: the bytes carry 120 frames at 5.000s, but the declared duration is
// 4.990s, so the conservative frame math reports 119. The fragment preparer is
// the authority on the actual tail; the plan must not reject the one-frame
// boundary before that authority runs.
func TestAlignFinalJobSegmentSourceWindowsAllowsOneFrameDurationRounding(t *testing.T) {
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
	if segment.SourceInUS != 0 || segment.SourceDurationUS != 5_000_000 {
		t.Fatalf("aligned window = %d + %d, want 0 + 5000000", segment.SourceInUS, segment.SourceDurationUS)
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
