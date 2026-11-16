package scriptgeneration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type countingSegmentPreparer struct {
	calls int
}

func (p *countingSegmentPreparer) PrepareFinalJobVideoSegment(_ context.Context, _ map[string]any, _ FinalJobVideoSegmentRequest) (map[string]any, error) {
	p.calls++
	return nil, nil
}

// stockBackedFinalJobPlan is the r14 shape: every scene carries a concrete
// stock binding, so every video segment references a Master-owned stock file.
func stockBackedFinalJobPlan(t *testing.T) (string, []any) {
	t.Helper()
	stockSHA := strings.Repeat("d", 64)
	audioSHA := strings.Repeat("e", 64)
	timelineSHA := strings.Repeat("b", 64)
	plan := finalJobCompiledPlan{
		PlanVersion: 2, TimelineRevision: 1, TimelineSHA256: timelineSHA, DurationUS: 5_000_000,
		FinalAudio: finalJobPlanAudio{AssetID: "final-audio", SHA256: audioSHA, SizeBytes: 10, DurationUS: 5_000_000},
		VideoTracks: []finalJobPlanVideoTrack{{TrackID: "main-video", Segments: []finalJobPlanSegment{{
			SegmentID: "segment-0003", AssetID: "stock-asset", SHA256: stockSHA,
			TimelineStartFrame: 0, FrameCount: 120, SourceInUS: 0, SourceDurationUS: 5_000_000,
		}}}},
		Assets: []finalJobPlanAsset{
			{AssetID: "stock-asset", SHA256: stockSHA, SizeBytes: 2048, Kind: "video", DurationUS: 5_000_000},
			{AssetID: "final-audio", SHA256: audioSHA, SizeBytes: 10, Kind: "final_audio", DurationUS: 5_000_000},
		},
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	runtimeAssets := []any{
		map[string]any{
			"asset_id": "stock-asset", "drive_file_id": "drive-stock", "sha256": stockSHA,
			"size_bytes": int64(2048), "kind": "video", "duration_ms": int64(5000), "role": "stock",
			"source_uri": "https://objectstore.example/stock-asset", "url": "velox-drive://drive-stock",
		},
		map[string]any{
			"asset_id": "final-audio", "sha256": audioSHA, "size_bytes": int64(10),
			"duration_ms": int64(5000), "role": "final_audio", "source_uri": "https://objectstore.example/final-audio",
		},
	}
	return string(raw), runtimeAssets
}

// TestPrepareFinalJobVideoSegmentsLeavesStockToMaster pins the r14 closure: a
// concrete stock binding is a Master-owned source, exactly like a
// stock_pool_slot. It must not be downloaded or cut locally and it must remain
// in the plan and in the runtime assets, otherwise the handoff fails after a
// successful render (`segment-0003 unexpectedly references stock`).
func TestPrepareFinalJobVideoSegmentsLeavesStockToMaster(t *testing.T) {
	rawPlan, runtimeAssets := stockBackedFinalJobPlan(t)
	preparer := &countingSegmentPreparer{}

	preparedPlan, preparedAssets, err := prepareFinalJobVideoSegments(context.Background(), rawPlan, runtimeAssets, preparer)
	if err != nil {
		t.Fatalf("prepareFinalJobVideoSegments: %v: a stock-backed plan must reach the Master", err)
	}
	if preparer.calls != 0 {
		t.Fatalf("preparer calls = %d, want 0: stock bytes must never enter the local fragment path", preparer.calls)
	}

	var plan finalJobCompiledPlan
	if err := json.Unmarshal([]byte(preparedPlan), &plan); err != nil {
		t.Fatalf("decode prepared plan: %v", err)
	}
	segment := plan.VideoTracks[0].Segments[0]
	if segment.AssetID != "stock-asset" || segment.SHA256 != strings.Repeat("d", 64) {
		t.Fatalf("segment binding = %q/%s, want the unchanged stock asset", segment.AssetID, segment.SHA256)
	}
	if segment.SourceInUS != 0 || segment.SourceDurationUS != 5_000_000 || segment.FrameCount != 120 {
		t.Fatalf("stock window = %d + %d over %d frames, want the untouched 0 + 5000000 over 120",
			segment.SourceInUS, segment.SourceDurationUS, segment.FrameCount)
	}
	var sawStock, sawAudio bool
	for _, asset := range plan.Assets {
		switch asset.AssetID {
		case "stock-asset":
			sawStock = asset.SHA256 == strings.Repeat("d", 64)
		case "final-audio":
			sawAudio = true
		}
	}
	if !sawStock {
		t.Fatalf("prepared plan lost the stock asset: %+v", plan.Assets)
	}
	if !sawAudio {
		t.Fatalf("prepared plan lost the final audio asset: %+v", plan.Assets)
	}

	var keptStock, keptAudio bool
	for _, raw := range preparedAssets {
		asset, _ := raw.(map[string]any)
		if asset == nil {
			continue
		}
		switch finalJobStringValue(asset["asset_id"]) {
		case "stock-asset":
			keptStock = strings.EqualFold(finalJobStringValue(asset["role"]), "stock") &&
				finalJobStringValue(asset["drive_file_id"]) == "drive-stock"
		case "final-audio":
			keptAudio = strings.EqualFold(finalJobStringValue(asset["role"]), "final_audio")
		}
	}
	if !keptStock {
		t.Fatalf("prepared runtime assets lost the stock Drive reference: %+v", preparedAssets)
	}
	if !keptAudio {
		t.Fatalf("prepared runtime assets lost the final audio reference: %+v", preparedAssets)
	}
}
