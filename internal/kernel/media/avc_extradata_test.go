package media

import (
	"testing"
)

// TestVerifyCanonicalVideoExtradata pins the publication gate on the bytes.
// The canonical fixture is encoded with the same effective profile the stock
// normalization pass and the fragment producer pin (libx264 veryfast, High
// 4.0, ref=1, fixed closed GOP, bt709 VUI); the drifted fixture keeps the same
// codec name, profile and geometry but changes the reference count and level,
// which is exactly the class the nominal profile checks cannot see.
// TestCanonicalVideoProfileRevisionIsPinned ties the human-readable encoder
// lane revision to the contract it claims to name. The revision is part of the
// fragment work identity, so changing it must never silently describe a
// different level, GOP or bitstream identity.
func TestCanonicalVideoProfileRevisionIsPinned(t *testing.T) {
	contract := DefaultAssemblyMediaContract()
	if CanonicalVideoProfileRevision != "velox-avc1-high40-ref1-bt709-v1" {
		t.Fatalf("canonical profile revision = %q", CanonicalVideoProfileRevision)
	}
	if contract.ID != AssemblyMediaContractID || contract.VideoLevel != "4.0" ||
		contract.KeyframeInterval != 48 || contract.BFrames != 0 || !contract.ClosedGOP ||
		contract.VideoExtradataSHA256 != CanonicalVideoExtradataSHA256 {
		t.Fatalf("revision %q no longer names the High 4.0 / refs 1 / GOP 48 contract", CanonicalVideoProfileRevision)
	}
}

// TestCanonicalFragmentsJoinAndDecode is the Producer-side junction
// certification: two independently encoded fragments must carry the exact same
// avcC, their packet-copy join must keep the exact frame count, and decoding
// across the join must not report an error. The drifted fragment is refused by
// the same gate that guards publication, so it can never reach the muxer.
