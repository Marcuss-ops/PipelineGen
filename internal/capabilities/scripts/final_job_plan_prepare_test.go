package scriptgeneration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type preparedSegmentStub struct {
	request FinalJobVideoSegmentRequest
	called  int
}

func (s *preparedSegmentStub) PrepareFinalJobVideoSegment(_ context.Context, source map[string]any, request FinalJobVideoSegmentRequest) (map[string]any, error) {
	s.request = request
	s.called++
	if source["asset_id"] != "stock-raw" {
		return nil, context.Canceled
	}
	return map[string]any{
		"asset_id": "fragment-0000", "source_uri": "https://objects.example/fragment",
		"role":   "stock",
		"sha256": strings.Repeat("b", 64), "size_bytes": int64(1024),
		"duration_us": int64(2_000_000), "frame_count": int64(48),
		"profile_id": "VELOX_ASSEMBLY_READY_V1", "copy_eligible": true,
		"first_frame_keyframe": true, "closed_gop": true,
		"timeline_revision": request.TimelineRevision, "timeline_sha256": request.TimelineSHA256,
		"timeline_start_frame": request.TimelineStartFrame,
	}, nil
}

func TestPrepareFinalJobVideoSegmentsReplacesRawSourceAndKeepsFinalAudio(t *testing.T) {
	timelineSHA := strings.Repeat("a", 64)
	plan := finalJobCompiledPlan{
		PlanVersion: 2, TimelineRevision: 1, TimelineSHA256: timelineSHA, DurationUS: 2_000_000,
		FinalAudio: finalJobPlanAudio{Mode: "FINAL_AUDIO_COPY", AssetID: "audio-1", SHA256: strings.Repeat("c", 64), SizeBytes: 32, DurationUS: 2_000_000},
		VideoTracks: []finalJobPlanVideoTrack{{TrackID: "main-video", Segments: []finalJobPlanSegment{{
			SegmentID: "segment-0000", AssetID: "stock-raw", SHA256: strings.Repeat("d", 64),
			TimelineStartFrame: 24, FrameCount: 48, SourceInUS: 500_000, SourceDurationUS: 2_000_000,
		}}}},
		Assets: []finalJobPlanAsset{
			{AssetID: "audio-1", SHA256: strings.Repeat("c", 64), SizeBytes: 32, Kind: "final_audio", DurationUS: 2_000_000},
			{AssetID: "stock-raw", SHA256: strings.Repeat("d", 64), SizeBytes: 2048, Kind: "video", DurationUS: 4_000_000},
		},
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	refs := []any{
		map[string]any{"asset_id": "stock-raw", "sha256": strings.Repeat("d", 64), "size_bytes": int64(2048), "source_uri": "https://objects.example/stock", "role": "stock"},
		map[string]any{"asset_id": "audio-1", "sha256": strings.Repeat("c", 64), "size_bytes": int64(32), "source_uri": "https://objects.example/audio", "role": "final_audio"},
	}
	preparer := &preparedSegmentStub{}
	prepared, runtimeAssets, err := prepareFinalJobVideoSegments(context.Background(), string(raw), refs, preparer)
	if err != nil {
		t.Fatalf("prepareFinalJobVideoSegments: %v", err)
	}
	if preparer.called != 1 || preparer.request.SourceInUS != 500_000 || preparer.request.TimelineStartFrame != 24 {
		t.Fatalf("preparer calls/request = %d/%+v", preparer.called, preparer.request)
	}
	var got finalJobCompiledPlan
	if err := json.Unmarshal([]byte(prepared), &got); err != nil {
		t.Fatal(err)
	}
	segment := got.VideoTracks[0].Segments[0]
	if segment.AssetID != "fragment-0000" || segment.SourceInUS != 0 || segment.SourceDurationUS != 2_000_000 || segment.SHA256 != strings.Repeat("b", 64) {
		t.Fatalf("prepared segment = %+v", segment)
	}
	if len(got.Assets) != 2 || got.Assets[0].AssetID != "audio-1" && got.Assets[1].AssetID != "audio-1" {
		t.Fatalf("compiled assets = %+v; want prepared fragment plus final audio", got.Assets)
	}
	if len(runtimeAssets) != 2 {
		t.Fatalf("runtime assets = %#v; want one prepared fragment and final audio", runtimeAssets)
	}
	for _, rawAsset := range runtimeAssets {
		asset := rawAsset.(map[string]any)
		if asset["asset_id"] == "stock-raw" {
			t.Fatal("raw stock source crossed the FINALIZE boundary")
		}
	}
}
