package scriptgeneration

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFinalJobCompiledPlanSplitsOverlayWindowsOnThe24FPSGrid(t *testing.T) {
	const videoSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const audioSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const overlaySHA = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	scenes := []map[string]any{{
		"duration_seconds": 5.0,
		"stock": map[string]any{
			"asset_id": "base-video", "sha256": videoSHA, "size_bytes": int64(1000),
			"duration_ms": int64(5000), "start_ms": int64(0),
		},
	}}
	overlayManifest := map[string]any{
		"asset": map[string]any{
			"asset_id": "overlay-timeline", "sha256": overlaySHA, "size_bytes": int64(500),
			"duration_ms": int64(5000), "frame_count": int64(120),
		},
		"entries": []any{map[string]any{"item_id": "phrase-1", "in_us": int64(1_000_000), "out_us": int64(2_000_000)}},
	}
	audio := FinalAudioReference{DurationUS: 5_000_000, DurationMS: 5000, CopyEligible: true, FinalMix: true, PlanSHA256: strings.Repeat("d", 64)}
	audioRef := map[string]any{"asset_id": "final-audio", "sha256": audioSHA, "size_bytes": int64(200), "duration_ms": int64(5000)}
	raw, sha, err := finalJobCompiledPlanPayload(scenes, nil, overlayManifest, audio, audioRef, 5_000_000)
	if err != nil {
		t.Fatalf("finalJobCompiledPlanPayload: %v", err)
	}
	if len(sha) != 64 {
		t.Fatalf("compiled plan SHA length = %d, want 64", len(sha))
	}
	var plan struct {
		PlanVersion int `json:"plan_version"`
		VideoTracks []struct {
			Segments []finalJobPlanSegment `json:"segments"`
		} `json:"video_tracks"`
		Assets []finalJobPlanAsset `json:"assets"`
	}
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		t.Fatalf("decode compiled plan: %v", err)
	}
	if plan.PlanVersion != 2 || len(plan.VideoTracks) != 1 {
		t.Fatalf("compiled plan version/tracks = %d/%d, want 2/1", plan.PlanVersion, len(plan.VideoTracks))
	}
	segments := plan.VideoTracks[0].Segments
	if len(segments) != 3 {
		t.Fatalf("segments = %+v, want base/overlay/base split", segments)
	}
	want := []struct {
		asset  string
		start  int64
		count  int64
		source int64
	}{
		{"base-video", 0, 24, 0},
		{"overlay-timeline", 24, 24, 1_000_000},
		{"base-video", 48, 72, 2_000_000},
	}
	for i, expected := range want {
		got := segments[i]
		if got.AssetID != expected.asset || got.TimelineStartFrame != expected.start || got.FrameCount != expected.count || got.SourceInUS != expected.source {
			t.Errorf("segment %d = %+v, want asset=%s start=%d frames=%d source=%d", i, got, expected.asset, expected.start, expected.count, expected.source)
		}
	}
	for i := 1; i < len(plan.Assets); i++ {
		if plan.Assets[i-1].AssetID > plan.Assets[i].AssetID {
			t.Fatalf("assets are not canonical-sorted: %q before %q", plan.Assets[i-1].AssetID, plan.Assets[i].AssetID)
		}
	}
}

func TestFinalJobCompiledPlanAlignsDurationToCertifiedAudio(t *testing.T) {
	const videoSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const audioSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	scenes := []map[string]any{{
		"duration_seconds": 19.704,
		"stock": map[string]any{
			"asset_id": "base-video", "sha256": videoSHA, "size_bytes": int64(1000),
			"duration_ms": int64(19704), "start_ms": int64(0),
		},
	}}
	audio := FinalAudioReference{
		DurationUS: 19_709_000, DurationMS: 19_709, CopyEligible: true, FinalMix: true,
		PlanSHA256: strings.Repeat("d", 64),
	}
	audioRef := map[string]any{"asset_id": "final-audio", "sha256": audioSHA, "size_bytes": int64(200), "duration_ms": int64(19709)}
	raw, _, err := finalJobCompiledPlanPayload(scenes, nil, nil, audio, audioRef, 19_704_000)
	if err != nil {
		t.Fatalf("finalJobCompiledPlanPayload: %v", err)
	}
	var plan struct {
		DurationUS int64 `json:"duration_us"`
		FinalAudio struct {
			DurationUS int64 `json:"duration_us"`
		} `json:"final_audio"`
	}
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		t.Fatalf("decode compiled plan: %v", err)
	}
	if plan.DurationUS != audio.DurationUS || plan.FinalAudio.DurationUS != audio.DurationUS {
		t.Fatalf("plan/audio durations = %d/%d, want certified audio duration %d", plan.DurationUS, plan.FinalAudio.DurationUS, audio.DurationUS)
	}
}
