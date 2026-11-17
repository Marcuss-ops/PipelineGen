package scriptgeneration

import "testing"

// TestRemoteFinalJobTimingKeepsSegmentPrepAcrossMerge pins the handoff block:
// the attach merge carries queue/execution/poll but has no fragment work of its
// own, so it must not erase the split the payload build measured.
func TestRemoteFinalJobTimingKeepsSegmentPrepAcrossMerge(t *testing.T) {
	base := &RemoteFinalJobTiming{
		HandoffMS: 48_960,
		PayloadMS: 48_000,
		SegmentPrep: &RemoteFinalJobSegmentPrepTiming{
			Segments: 35, Copied: 30, EncodeMS: 31_200, PublishMS: 9_800, Bytes: 250_000_000,
		},
	}
	merged := base.Merge(&RemoteFinalJobTiming{ExecutionMS: 7_428, WaitMS: 36_886})
	if merged.SegmentPrep == nil {
		t.Fatal("attach merge erased the handoff's segment_prep block")
	}
	if merged.SegmentPrep.Segments != 35 || merged.SegmentPrep.EncodeMS != 31_200 || merged.SegmentPrep.PublishMS != 9_800 {
		t.Fatalf("segment_prep = %+v, want the measured handoff split", merged.SegmentPrep)
	}
	if merged.ExecutionMS != 7_428 {
		t.Fatalf("execution_ms = %d, want the attach phase preserved", merged.ExecutionMS)
	}
	// A later observation that DID prepare fragments replaces the block instead
	// of mixing two handoffs' numbers.
	next := merged.Merge(&RemoteFinalJobTiming{SegmentPrep: &RemoteFinalJobSegmentPrepTiming{Segments: 2, Reused: 2}})
	if next.SegmentPrep == nil || next.SegmentPrep.Segments != 2 || next.SegmentPrep.Reused != 2 {
		t.Fatalf("new handoff segment_prep = %+v, want the newer observation", next.SegmentPrep)
	}
}
