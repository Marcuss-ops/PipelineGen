package finaljob

import "testing"

// TestFinalJobSegmentReasonSummaryNamesEveryAuthority pins the local segment
// provenance report: the compiled plan's placements are grouped by who owns
// the bytes, so a remote encode count can be reconciled with a reason.
func TestFinalJobSegmentReasonSummaryNamesEveryAuthority(t *testing.T) {
	plan := FinalJobCompiledPlan{
		VideoTracks: []FinalJobPlanVideoTrack{{Segments: []FinalJobPlanSegment{
			{SegmentID: "segment-0000", StockPoolSlot: true},
			{SegmentID: "segment-0001", AssetID: "asset-stock"},
			{SegmentID: "segment-0002", AssetID: "asset-clip"},
			{SegmentID: "segment-0003", AssetID: "asset-overlay"},
			{SegmentID: "segment-0004", AssetID: "asset-timeline"},
			{SegmentID: "segment-0005", AssetID: "asset-scene"},
		}}},
	}
	refs := map[string]map[string]any{
		"asset-stock":    {"role": "stock"},
		"asset-clip":     {"role": "clip"},
		"asset-overlay":  {"role": "overlay"},
		"asset-timeline": {"role": "overlay_timeline"},
		"asset-scene":    {"role": "video"},
	}

	got := finalJobSegmentReasonSummary(plan, refs)
	want := map[string]int{
		FinalJobSegmentReasonStockPool:       1,
		FinalJobSegmentReasonStockBound:      1,
		FinalJobSegmentReasonClip:            1,
		FinalJobSegmentReasonOverlay:         1,
		FinalJobSegmentReasonOverlayTimeline: 1,
		FinalJobSegmentReasonScene:           1,
	}
	if len(got) != len(want) {
		t.Fatalf("summary = %v, want %v", got, want)
	}
	for reason, count := range want {
		if got[reason] != count {
			t.Fatalf("summary[%q] = %d, want %d (full: %v)", reason, got[reason], count, got)
		}
	}
}
