package finaljob

import (
	"strings"
	"testing"

	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/finaljob"
)

func TestStockSegmentsUseFrameExactWindowsAndRepeatTheBoundRange(t *testing.T) {
	const sha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	scenes := []FinalJobRemoteScene{{
		SceneID: "scene-1", Kind: "stock", DurationSeconds: 20,
		Stock: map[string]any{
			"asset_id": "stock-1", "sha256": sha, "size_bytes": int64(100),
			"duration_ms": int64(6000), "start_ms": int64(500),
		},
	}}
	assets := &finalJobPlanAssets{}
	base, err := finalJobPlanBaseSegments(scenes, assets, map[string]int64{"scene-1": 5000})
	if err != nil {
		t.Fatal(err)
	}
	segments, nextFrame, err := finalJobAppendBaseSegments(base, 0, 20_000_000, nil, 0, 24, 1)
	if err != nil {
		t.Fatal(err)
	}
	if nextFrame != 480 || len(segments) != 4 {
		t.Fatalf("coverage = %d frames across %d segments, want 480 across 4", nextFrame, len(segments))
	}
	wantFrames := []int64{120, 120, 120, 120}
	wantSourceUS := []int64{5_000_000, 5_000_000, 5_000_000, 5_000_000}
	for i, segment := range segments {
		if segment.FrameCount != wantFrames[i] {
			t.Errorf("segment %d frame_count = %d, want %d", i, segment.FrameCount, wantFrames[i])
		}
		if segment.SourceDurationUS != wantSourceUS[i] {
			t.Errorf("segment %d source_duration_us = %d, want %d", i, segment.SourceDurationUS, wantSourceUS[i])
		}
		if segment.SourceInUS != 500_000 {
			t.Errorf("segment %d source_in_us = %d, want the selected stock start 500000", i, segment.SourceInUS)
		}
	}
}

func TestNonFrameAlignedStockWindowRepeatsInFrameExactChunks(t *testing.T) {
	const sha = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	scenes := []FinalJobRemoteScene{{
		SceneID: "scene-1", Kind: "stock", DurationSeconds: 1300.0 / 24.0,
		Stock: map[string]any{
			"asset_id": "stock-1", "sha256": sha, "size_bytes": int64(100),
			"duration_ms": int64(60_000),
		},
	}}
	base, err := finalJobPlanBaseSegments(scenes, &finalJobPlanAssets{}, map[string]int64{"scene-1": 54_164})
	if err != nil {
		t.Fatal(err)
	}
	segments, nextFrame, err := finalJobAppendBaseSegments(base, 0, FinalJobFrameStartUS(1300, 24, 1), nil, 0, 24, 1)
	if err != nil {
		t.Fatal(err)
	}
	if nextFrame != 1300 || len(segments) != 2 {
		t.Fatalf("coverage = %d frames across %d segments, want 1300 across 2", nextFrame, len(segments))
	}
	if segments[0].FrameCount != 1299 || segments[0].SourceDurationUS != 54_125_000 {
		t.Fatalf("first source pass = %+v, want 1299 frame / 54125000us", segments[0])
	}
	if segments[1].FrameCount != 1 || segments[1].SourceDurationUS != 41_666 {
		t.Fatalf("repeated tail = %+v, want 1 frame / 41666us", segments[1])
	}
}

func TestReportedGratteriDurationMismatchesAreReconciledBeforeHandoff(t *testing.T) {
	cases := []struct {
		name           string
		boundMS        int64
		assetMS        int64
		timelineFrames int64
	}{
		{name: "r15", boundMS: 5_000, assetMS: 8_000, timelineFrames: 179},
		{name: "r22", boundMS: 54_164, assetMS: 60_000, timelineFrames: 1_233},
		{name: "r28", boundMS: 10_250, assetMS: 20_000, timelineFrames: 220},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sha := strings.Repeat("a", 64)
			scene := FinalJobRemoteScene{
				SceneID: "scene-1", Kind: "stock",
				DurationSeconds: float64(tc.timelineFrames) / 24,
				Stock: map[string]any{
					"asset_id": "stock-1", "sha256": sha, "size_bytes": int64(100),
					"duration_ms": tc.assetMS,
				},
			}
			base, err := finalJobPlanBaseSegments([]FinalJobRemoteScene{scene}, &finalJobPlanAssets{}, map[string]int64{"scene-1": tc.boundMS})
			if err != nil {
				t.Fatal(err)
			}
			endUS := FinalJobFrameStartUS(tc.timelineFrames, 24, 1)
			segments, nextFrame, err := finalJobAppendBaseSegments(base, 0, endUS, nil, 0, 24, 1)
			if err != nil {
				t.Fatal(err)
			}
			if nextFrame != tc.timelineFrames {
				t.Fatalf("coverage = %d frames, want %d", nextFrame, tc.timelineFrames)
			}
			var covered int64
			for _, segment := range segments {
				covered += segment.FrameCount
				if want := kerneljob.FrameDurationUSAtFPS(segment.FrameCount, 24, 1); segment.SourceDurationUS != want {
					t.Errorf("%s segment %s source_duration_us=%d, want frame-exact %d", tc.name, segment.SegmentID, segment.SourceDurationUS, want)
				}
				if segment.SourceInUS+segment.SourceDurationUS > tc.assetMS*1000 {
					t.Errorf("%s segment %s exceeds the saved asset extent", tc.name, segment.SegmentID)
				}
			}
			if covered != tc.timelineFrames {
				t.Fatalf("segments cover %d frames, want %d", covered, tc.timelineFrames)
			}
		})
	}
}

func TestStockPlanShorterThanWindowIsTrimmedToFrameCount(t *testing.T) {
	const sha = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	scenes := []FinalJobRemoteScene{{
		SceneID: "scene-1", Kind: "stock", DurationSeconds: 220.0 / 24.0,
		Stock: map[string]any{
			"asset_id": "stock-1", "sha256": sha, "size_bytes": int64(100),
			"duration_us": int64(10_250_000), "duration_ms": int64(10_250),
		},
	}}
	assets := &finalJobPlanAssets{}
	base, err := finalJobPlanBaseSegments(scenes, assets, map[string]int64{"scene-1": 10_250})
	if err != nil {
		t.Fatal(err)
	}
	segments, nextFrame, err := finalJobAppendBaseSegments(base, 0, 9_166_667, nil, 0, 24, 1)
	if err != nil {
		t.Fatal(err)
	}
	if nextFrame != 220 || len(segments) != 1 {
		t.Fatalf("coverage = %d frames across %d segments, want 220 across 1", nextFrame, len(segments))
	}
	if got, want := segments[0].SourceDurationUS, int64(9_166_666); got != want {
		t.Fatalf("source_duration_us = %d, want frame-exact %d", got, want)
	}
}

func TestStockWindowRecoversFrameFromFlooredMicrosecondDuration(t *testing.T) {
	const sha = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	scenes := []FinalJobRemoteScene{{
		SceneID: "scene-1", Kind: "stock", DurationSeconds: 220.0 / 24.0,
		Stock: map[string]any{
			"asset_id": "stock-1", "sha256": sha, "size_bytes": int64(100),
			"duration_us": int64(9_166_666), "duration_ms": int64(9_166),
		},
	}}
	base, err := finalJobPlanBaseSegments(scenes, &finalJobPlanAssets{}, map[string]int64{"scene-1": 9_167})
	if err != nil {
		t.Fatal(err)
	}
	segments, nextFrame, err := finalJobAppendBaseSegments(base, 0, FinalJobFrameStartUS(220, 24, 1), nil, 0, 24, 1)
	if err != nil {
		t.Fatal(err)
	}
	if nextFrame != 220 || len(segments) != 1 {
		t.Fatalf("coverage = %d frames across %d segments, want 220 across 1", nextFrame, len(segments))
	}
	if got, want := segments[0].FrameCount, int64(220); got != want {
		t.Fatalf("frame_count = %d, want %d", got, want)
	}
	if got, want := segments[0].SourceDurationUS, int64(9_166_666); got != want {
		t.Fatalf("source_duration_us = %d, want %d", got, want)
	}
}

func TestStockPlanKeepsSubMillisecondSavedDuration(t *testing.T) {
	const sha = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	scenes := []FinalJobRemoteScene{{
		SceneID: "scene-1", Kind: "stock", DurationSeconds: 5,
		Stock: map[string]any{
			"asset_id": "stock-1", "sha256": sha, "size_bytes": int64(100),
			"duration_us": int64(7_457_900), "duration_ms": int64(7_457),
		},
	}}
	assets := &finalJobPlanAssets{}
	base, err := finalJobPlanBaseSegments(scenes, assets, map[string]int64{"scene-1": 7_458})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := base[0].SourceDurationUS, int64(7_457_900); got != want {
		t.Fatalf("source_duration_us = %d, want precise saved extent %d", got, want)
	}
	segments, _, err := finalJobAppendBaseSegments(base, 0, 5_000_000, nil, 0, 24, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 || segments[0].SourceDurationUS != 5_000_000 {
		t.Fatalf("frame-exact plan segments = %+v, want one 5-second source window", segments)
	}
}

func TestFinalJobRetainPlanAssetsOnlyTreatsStockScenesAsSourceLocked(t *testing.T) {
	assets := &finalJobPlanAssets{}
	if err := assets.add(FinalJobPlanAsset{
		AssetID: "clip-1", SHA256: strings.Repeat("c", 64), SizeBytes: 100,
		Kind: "video", DurationUS: 5_000_000,
	}); err != nil {
		t.Fatal(err)
	}
	segments := []FinalJobPlanSegment{{AssetID: "clip-1", SourceDurationUS: 1_000_000}}
	ids := finalJobRetainPlanAssets([]FinalJobRemoteScene{
		{Kind: "stock", Stock: map[string]any{"asset_id": "stock-1"}},
		{Kind: "clip", Stock: map[string]any{"asset_id": "clip-1"}},
		{Kind: "overlay", Stock: map[string]any{"asset_id": "overlay-1"}},
	}, segments, assets, "")
	if len(ids) != 1 {
		t.Fatalf("immutable source ids = %v, want only the concrete stock scene", ids)
	}
	if _, ok := ids["stock-1"]; !ok {
		t.Fatalf("stock asset was not retained as source-locked: %v", ids)
	}
	if _, ok := ids["clip-1"]; ok {
		t.Fatal("clip asset was incorrectly treated as an immutable stock window")
	}
}
