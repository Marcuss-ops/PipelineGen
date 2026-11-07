package scriptgeneration

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestFinalJobCompiledPlanRejectsStockSceneThatDoesNotMatchTheWholeFile pins
// the r53/r56 closure: a 5.000s scene may never be built against 4.979s stock
// bytes. The old plan padded the source extent to 5.000s and the Master
// rejected the out-of-bounds/partial window; the Producer now fails first.
func TestFinalJobCompiledPlanRejectsStockSceneThatDoesNotMatchTheWholeFile(t *testing.T) {
	const videoSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const audioSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	scenes := []map[string]any{{
		"duration_seconds": 5.0,
		"kind":             "stock",
		"stock": map[string]any{
			"asset_id": "base-video", "sha256": videoSHA, "size_bytes": int64(1000),
			"duration_ms": int64(4979), "start_ms": int64(0),
		},
	}}
	audio := FinalAudioReference{DurationUS: 5_000_000, DurationMS: 5000, CopyEligible: true, FinalMix: true, PlanSHA256: strings.Repeat("d", 64)}
	audioRef := map[string]any{"asset_id": "final-audio", "sha256": audioSHA, "size_bytes": int64(200), "duration_ms": int64(5000)}
	_, _, err := finalJobCompiledPlanPayload(scenes, nil, nil, audio, audioRef, 5_000_000)
	if err == nil || !strings.Contains(err.Error(), "whole stock file") {
		t.Fatalf("finalJobCompiledPlanPayload error = %v, want a whole-stock-file mismatch", err)
	}
}

// TestFinalJobCompiledPlanKeepsAWholeStockFileExact is the green twin: a 5.000s
// scene backed by a 5.000s stock file produces exactly one whole-file window
// with the real duration and frame count.
func TestFinalJobCompiledPlanKeepsAWholeStockFileExact(t *testing.T) {
	const videoSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const audioSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	scenes := []map[string]any{{
		"duration_seconds": 5.0,
		"kind":             "stock",
		"stock": map[string]any{
			"asset_id": "base-video", "sha256": videoSHA, "size_bytes": int64(1000),
			"duration_ms": int64(5000), "start_ms": int64(0),
		},
	}}
	audio := FinalAudioReference{DurationUS: 5_000_000, DurationMS: 5000, CopyEligible: true, FinalMix: true, PlanSHA256: strings.Repeat("d", 64)}
	audioRef := map[string]any{"asset_id": "final-audio", "sha256": audioSHA, "size_bytes": int64(200), "duration_ms": int64(5000)}
	raw, _, err := finalJobCompiledPlanPayload(scenes, nil, nil, audio, audioRef, 5_000_000)
	if err != nil {
		t.Fatalf("finalJobCompiledPlanPayload: %v", err)
	}
	var plan struct {
		Assets []finalJobPlanAsset `json:"assets"`
		Tracks []struct {
			Segments []finalJobPlanSegment `json:"segments"`
		} `json:"video_tracks"`
	}
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		t.Fatal(err)
	}
	var got finalJobPlanAsset
	for _, asset := range plan.Assets {
		if asset.AssetID == "base-video" {
			got = asset
		}
	}
	if got.DurationUS != 5_000_000 {
		t.Fatalf("stock source extent = %d, want the exact 5s file duration", got.DurationUS)
	}
	if len(plan.Tracks) != 1 || len(plan.Tracks[0].Segments) != 1 {
		t.Fatalf("segments = %+v, want one stock segment", plan.Tracks)
	}
	if segment := plan.Tracks[0].Segments[0]; segment.SourceInUS != 0 || segment.SourceDurationUS != 5_000_000 || segment.FrameCount != 120 {
		t.Fatalf("stock segment = %d + %d over %d frames, want 0 + 5000000 over 120", segment.SourceInUS, segment.SourceDurationUS, segment.FrameCount)
	}
}

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

// r36 sent three overlay windows a few milliseconds past the end of their
// artifact and the Master rejected the whole plan after six minutes of work.
// The plan must clamp them instead of asking the worker for frames that do not
// exist.
func TestFinalJobCompiledPlanClampsOverlayWindowsToArtifactDuration(t *testing.T) {
	const videoSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const audioSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const overlaySHA = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	scenes := []map[string]any{{
		"duration_seconds": 20.0,
		"stock": map[string]any{
			"asset_id": "base-video", "sha256": videoSHA, "size_bytes": int64(1000),
			"duration_ms": int64(20000), "start_ms": int64(0),
		},
	}}
	overlayManifest := map[string]any{
		"asset": map[string]any{
			"asset_id": "overlay-15s", "sha256": overlaySHA, "size_bytes": int64(500),
			"duration_ms": int64(15000), "frame_count": int64(360),
		},
		"entries": []any{map[string]any{
			"item_id": "phrase-late",
			"in_us":   int64(14_000_000), "out_us": int64(15_013_000),
			"artifact_in_us": int64(14_000_000), "artifact_out_us": int64(15_013_000),
		}},
	}
	audio := FinalAudioReference{DurationUS: 20_000_000, DurationMS: 20000, CopyEligible: true, FinalMix: true, PlanSHA256: strings.Repeat("d", 64)}
	audioRef := map[string]any{"asset_id": "final-audio", "sha256": audioSHA, "size_bytes": int64(200), "duration_ms": int64(20000)}
	raw, _, err := finalJobCompiledPlanPayload(scenes, nil, overlayManifest, audio, audioRef, 20_000_000)
	if err != nil {
		t.Fatalf("windows must be clamped, not rejected: %v", err)
	}
	var plan struct {
		VideoTracks []struct {
			Segments []finalJobPlanSegment `json:"segments"`
		} `json:"video_tracks"`
		Assets []finalJobPlanAsset `json:"assets"`
	}
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		t.Fatalf("decode compiled plan: %v", err)
	}
	durationByAsset := make(map[string]int64, len(plan.Assets))
	for _, asset := range plan.Assets {
		durationByAsset[asset.AssetID] = asset.DurationUS
	}
	var overlay *finalJobPlanSegment
	for i := range plan.VideoTracks[0].Segments {
		segment := &plan.VideoTracks[0].Segments[i]
		if limit, ok := durationByAsset[segment.AssetID]; ok && segment.SourceInUS+segment.SourceDurationUS > limit {
			t.Fatalf("segment %q = %d + %d exceeds asset duration %d", segment.SegmentID, segment.SourceInUS, segment.SourceDurationUS, limit)
		}
		if segment.AssetID == "overlay-15s" {
			overlay = segment
		}
	}
	if overlay == nil {
		t.Fatal("compiled plan has no overlay segment")
	}
	if overlay.SourceInUS != 14_000_000 || overlay.SourceDurationUS != 1_000_000 {
		t.Fatalf("overlay source window = %d + %d, want 14000000 + 1000000", overlay.SourceInUS, overlay.SourceDurationUS)
	}
	if overlay.TimelineStartFrame != 336 || overlay.FrameCount != 24 {
		t.Fatalf("overlay timeline = frame %d + %d, want 336 + 24", overlay.TimelineStartFrame, overlay.FrameCount)
	}
}

// Exact replay of the three ranges the Master rejected in r36: every window
// ends 4-20 ms past the artifact and must be trimmed to the certified length
// instead of failing the whole plan after the local handoff has been paid for.
func TestFinalJobCompiledPlanClampsTheThreeR36Windows(t *testing.T) {
	const videoSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const audioSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	scenes := []map[string]any{{
		"duration_seconds": 1200.0,
		"stock": map[string]any{
			"asset_id": "base-video", "sha256": videoSHA, "size_bytes": int64(1000),
			"duration_ms": int64(1_200_000), "start_ms": int64(0),
		},
	}}
	cases := []struct {
		id       string
		sha      string
		assetUS  int64
		inUS     int64
		windowUS int64
		startUS  int64
	}{
		{"overlay-15s", strings.Repeat("1", 64), 15_000_000, 14_000_000, 1_013_000, 100_000_000},
		{"overlay-26625ms", strings.Repeat("2", 64), 26_625_000, 20_000_000, 6_645_000, 400_000_000},
		{"overlay-25209ms", strings.Repeat("3", 64), 25_209_000, 24_000_000, 1_213_000, 800_000_000},
	}
	manifests := make([]any, 0, len(cases))
	for _, c := range cases {
		manifests = append(manifests, map[string]any{
			"asset": map[string]any{
				"asset_id": c.id, "sha256": c.sha, "size_bytes": int64(500),
				"duration_ms": c.assetUS / 1000,
			},
			"entries": []any{map[string]any{
				"item_id": c.id + "-entry",
				"in_us":   c.startUS, "out_us": c.startUS + c.windowUS,
				"artifact_in_us": c.inUS, "artifact_out_us": c.inUS + c.windowUS,
			}},
		})
	}
	overlayManifest := map[string]any{"schema_version": "pipelinegen.overlay-manifest-set.v1", "manifests": manifests}
	audio := FinalAudioReference{DurationUS: 1_200_000_000, DurationMS: 1_200_000, CopyEligible: true, FinalMix: true, PlanSHA256: strings.Repeat("d", 64)}
	audioRef := map[string]any{"asset_id": "final-audio", "sha256": audioSHA, "size_bytes": int64(200), "duration_ms": int64(1_200_000)}
	raw, _, err := finalJobCompiledPlanPayload(scenes, nil, overlayManifest, audio, audioRef, 1_200_000_000)
	if err != nil {
		t.Fatalf("r36 windows must be clamped, not rejected: %v", err)
	}
	var plan struct {
		VideoTracks []struct {
			Segments []finalJobPlanSegment `json:"segments"`
		} `json:"video_tracks"`
		Assets []finalJobPlanAsset `json:"assets"`
	}
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		t.Fatalf("decode compiled plan: %v", err)
	}
	durationByAsset := make(map[string]int64, len(plan.Assets))
	for _, asset := range plan.Assets {
		durationByAsset[asset.AssetID] = asset.DurationUS
	}
	clamped := make(map[string]int64, len(cases))
	for _, segment := range plan.VideoTracks[0].Segments {
		if limit := durationByAsset[segment.AssetID]; limit > 0 && segment.SourceInUS+segment.SourceDurationUS > limit {
			t.Fatalf("segment %q = %d + %d exceeds asset duration %d", segment.SegmentID, segment.SourceInUS, segment.SourceDurationUS, limit)
		}
		clamped[segment.AssetID] = segment.SourceDurationUS
	}
	for _, c := range cases {
		want := c.assetUS - c.inUS
		if got := clamped[c.id]; got != want {
			t.Fatalf("%s source duration = %d, want %d (trimmed to the artifact)", c.id, got, want)
		}
	}
}

// Older renderer manifests carry neither duration_ms nor frame_count. The
// artifact's own entries are then the only admissible evidence: the declared
// length is the furthest point an entry claims, never the whole timeline.
func TestFinalJobCompiledPlanDerivesOverlayDurationFromEntryTiming(t *testing.T) {
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
		"asset":   map[string]any{"asset_id": "overlay-legacy", "sha256": overlaySHA, "size_bytes": int64(500)},
		"entries": []any{map[string]any{"item_id": "phrase", "in_us": int64(1_000_000), "out_us": int64(2_000_000)}},
	}
	audio := FinalAudioReference{DurationUS: 5_000_000, DurationMS: 5000, CopyEligible: true, FinalMix: true, PlanSHA256: strings.Repeat("d", 64)}
	audioRef := map[string]any{"asset_id": "final-audio", "sha256": audioSHA, "size_bytes": int64(200), "duration_ms": int64(5000)}
	raw, _, err := finalJobCompiledPlanPayload(scenes, nil, overlayManifest, audio, audioRef, 5_000_000)
	if err != nil {
		t.Fatalf("entry timing must make a legacy manifest projectable: %v", err)
	}
	var plan struct {
		VideoTracks []struct {
			Segments []finalJobPlanSegment `json:"segments"`
		} `json:"video_tracks"`
	}
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		t.Fatalf("decode compiled plan: %v", err)
	}
	for _, segment := range plan.VideoTracks[0].Segments {
		if segment.AssetID == "overlay-legacy" {
			if segment.SourceInUS != 1_000_000 || segment.SourceDurationUS != 1_000_000 {
				t.Fatalf("legacy overlay window = %d + %d, want 1000000 + 1000000", segment.SourceInUS, segment.SourceDurationUS)
			}
			return
		}
	}
	t.Fatal("compiled plan has no legacy overlay segment")
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
