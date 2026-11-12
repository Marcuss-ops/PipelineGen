package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// conformanceFixtureSHA256 pins the byte identity of the fixture shared with
// RenderingGen's contracts/. If either repository edits its copy without the
// other, this test fails before a producer/consumer drift can ship.
const conformanceFixtureSHA256 = "c8b8195de94e39b3ff36ffba938f4941a3e929ce0bdb1fdd0c8b37e445088faa"

// TestChainSegmentsSharedConformanceFixture is the producer half of the
// cross-repository chain_segments contract: five images, five independent
// motions, five preserved asset identities, one validated plan.
func TestChainSegmentsSharedConformanceFixture(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "testdata", "contracts", "chain-segments-v1.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read shared conformance fixture: %v", err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != conformanceFixtureSHA256 {
		t.Fatalf("shared fixture sha256 = %s, want %s; update both repositories together", got, conformanceFixtureSHA256)
	}
	var document struct {
		Plan OverlayPlan `json:"plan"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode conformance plan: %v", err)
	}
	if err := document.Plan.Validate(); err != nil {
		t.Fatalf("producer rejected the shared conformance plan: %v", err)
	}
	if len(document.Plan.Items) != 1 {
		t.Fatalf("conformance plan items = %d, want 1", len(document.Plan.Items))
	}
	segments, ok := DecodeChainSegments(document.Plan.Items[0].Params)
	if !ok || len(segments) != 5 {
		t.Fatalf("conformance chain segments = %+v, want five", document.Plan.Items[0].Params["chain_segments"])
	}
	assets := make(map[string]bool, len(segments))
	motions := make(map[string]bool, len(segments))
	for index, segment := range segments {
		if segment.AssetID == "" || len(segment.Assets) != 1 || segment.Assets[0].AssetID != segment.AssetID {
			t.Fatalf("segment %d lost its own asset identity: %+v", index, segment)
		}
		if segment.MotionID == "" {
			t.Fatalf("segment %d has no independent motion", index)
		}
		if assets[segment.AssetID] {
			t.Fatalf("segment %d repeats asset %q", index, segment.AssetID)
		}
		if motions[segment.MotionID] {
			t.Fatalf("segment %d repeats motion %q", index, segment.MotionID)
		}
		assets[segment.AssetID] = true
		motions[segment.MotionID] = true
	}
}
