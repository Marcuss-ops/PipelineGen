package wiring

import "testing"

// TestFinalJobReceiptWindowCarriesSourceIdentity pins the diagnostic fields that
// make a slow window attributable: without the source SHA and the normalized
// flag a 12 s encode cannot be told apart from a 3 s one after the temporary
// source is gone (r96: segment-0084 12,0 s, segment-0035 10,7 s, segment-0020
// 10,6 s against a 3,3 s median for the same frame count).
func TestFinalJobReceiptWindowCarriesSourceIdentity(t *testing.T) {
	stats := finalJobSegmentStats{windows: []finalJobSegmentWindow{{
		segmentID:  "segment-0084",
		sourceSHA:  "3f2a1c",
		normalized: true,
		path:       finalJobWindowEncode,
		frames:     120,
		encodeMS:   12048,
	}}}
	receipt := stats.timing()
	if len(receipt.Windows) != 1 {
		t.Fatalf("windows = %d, want 1", len(receipt.Windows))
	}
	got := receipt.Windows[0]
	if got.SourceSHA256 != "3f2a1c" {
		t.Fatalf("source_sha256 = %q, want the source identity", got.SourceSHA256)
	}
	if !got.Normalized {
		t.Fatalf("normalized = false, want true for an encode that rebuilt geometry")
	}
	if got.EncodeMS != 12048 || got.Frames != 120 {
		t.Fatalf("window = %+v, want the measured encode preserved", got)
	}
}
