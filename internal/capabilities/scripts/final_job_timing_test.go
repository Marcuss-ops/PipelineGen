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

// TestRemoteFinalJobTimingKeepsArtifactDownloadAcrossMerge pins the transfer
// block: the attach observes Master phases only, so merging it must not erase
// the download the adapter measured while materializing the artifact.
func TestRemoteFinalJobTimingKeepsArtifactDownloadAcrossMerge(t *testing.T) {
	base := &RemoteFinalJobTiming{
		HandoffMS:             22_197,
		ArtifactDownloadMS:    257_400,
		ArtifactDownloadBytes: 891_734_780,
	}
	merged := base.Merge(&RemoteFinalJobTiming{QueueMS: 601_000, ExecutionMS: 26_473, WaitMS: 626_622})
	if merged.ArtifactDownloadMS != 257_400 || merged.ArtifactDownloadBytes != 891_734_780 {
		t.Fatalf("artifact download lost across merge: %+v", merged)
	}
	if merged.HandoffMS != 22_197 || merged.QueueMS != 601_000 {
		t.Fatalf("merge must keep both sides: %+v", merged)
	}
	// Reuse is monotone: a later attach can only see the deterministic temp
	// file already verified.
	reused := merged.Merge(&RemoteFinalJobTiming{ArtifactDownloadReused: true})
	if !reused.ArtifactDownloadReused {
		t.Fatal("artifact_download_reused must survive a later attach merge")
	}
	if again := reused.Merge(&RemoteFinalJobTiming{ArtifactDownloadMS: 12}); again.ArtifactDownloadReused != true {
		t.Fatal("a measured transfer must not clear a proven reuse")
	}
}
