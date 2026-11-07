package wiring

import (
	"context"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// TestFinalJobPreparerRejectsLocalStock pins the removal of the local stock
// path. The remote Master owns stock processing; a local source reference must
// never reach the fragment encoder again.
func TestFinalJobPreparerRejectsLocalStock(t *testing.T) {
	preparer := newFinalJobSegmentPreparer("", mediaexec.EncoderPolicy{}, "", 1)
	source := map[string]any{
		"asset_id": "stock-1", "sha256": strings.Repeat("a", 64), "size_bytes": int64(1024),
		"local_source_path": "/tmp/stock.mp4", "role": "stock",
	}
	request := scriptgen.FinalJobVideoSegmentRequest{
		SegmentID: "segment-0000", SourceInUS: 0, SourceDurationUS: 1_000_000,
		TimelineStartFrame: 0, FrameCount: 24, TimelineRevision: 1, TimelineSHA256: strings.Repeat("b", 64),
	}
	_, err := preparer.PrepareFinalJobVideoSegment(context.Background(), source, request)
	if err == nil || !strings.Contains(err.Error(), "remote Master") {
		t.Fatalf("PrepareFinalJobVideoSegment error = %v, want the remote-Master stock rejection", err)
	}
}
