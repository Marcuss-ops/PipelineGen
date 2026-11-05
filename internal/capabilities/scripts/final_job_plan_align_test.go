package scriptgeneration

import (
	"encoding/json"
	"strings"
	"testing"

	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

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
