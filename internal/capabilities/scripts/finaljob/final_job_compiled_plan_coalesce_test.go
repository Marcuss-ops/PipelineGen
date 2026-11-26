package finaljob

import "testing"

// TestFinalJobMergeContiguousSegmentsCollapsesOnlyContinuousWindows pins the
// preventive coalescing: neighboring placements of one asset merge only when
// both the output timeline and the source window are continuous. A source
// reset or an asset change is left as a separate segment, and ids stay dense.
func TestFinalJobMergeContiguousSegmentsCollapsesOnlyContinuousWindows(t *testing.T) {
	const shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	input := []FinalJobPlanSegment{
		{SegmentID: "segment-0000", AssetID: "asset-a", SHA256: shaA, TimelineStartFrame: 0, FrameCount: 24, SourceInUS: 0, SourceDurationUS: 1_000_000},
		{SegmentID: "segment-0001", AssetID: "asset-a", SHA256: shaA, TimelineStartFrame: 24, FrameCount: 24, SourceInUS: 1_000_000, SourceDurationUS: 1_000_000},
		{SegmentID: "segment-0002", AssetID: "asset-a", SHA256: shaA, TimelineStartFrame: 48, FrameCount: 24, SourceInUS: 0, SourceDurationUS: 1_000_000},
		{SegmentID: "segment-0003", AssetID: "asset-b", SHA256: shaB, TimelineStartFrame: 72, FrameCount: 24, SourceInUS: 0, SourceDurationUS: 1_000_000},
	}
	got := finalJobMergeContiguousSegments(input)
	if len(got) != 3 {
		t.Fatalf("merged segments = %d (%+v), want 3", len(got), got)
	}
	if got[0].FrameCount != 48 || got[0].SourceDurationUS != 2_000_000 || got[0].SourceInUS != 0 {
		t.Fatalf("merged first segment = %+v, want 48 frames / 2s source from offset 0", got[0])
	}
	if got[1].AssetID != "asset-a" || got[1].SourceInUS != 0 {
		t.Fatalf("second segment = %+v, want the non-contiguous source reset kept separate", got[1])
	}
	if got[2].AssetID != "asset-b" {
		t.Fatalf("third segment = %+v, want the asset change kept separate", got[2])
	}
	wantIDs := []string{"segment-0000", "segment-0001", "segment-0002"}
	for i, want := range wantIDs {
		if got[i].SegmentID != want {
			t.Fatalf("segment %d id = %q, want %q", i, got[i].SegmentID, want)
		}
	}
}
