package postgres

import (
	"testing"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// TestNormalizeJobTypeEmptyWarnsAndCounts pins the fail-loud contract: an
// empty job_type normalizes to render_segment (never silently) and bumps
// EmptyJobTypeNormalizedTotal; unknown types are rejected.
func TestNormalizeJobTypeEmptyWarnsAndCounts(t *testing.T) {
	before := EmptyJobTypeNormalizedTotal()
	got, err := normalizeJobType("")
	if err != nil {
		t.Fatalf("normalize empty: %v", err)
	}
	if got != model.JobTypeRenderSegment {
		t.Fatalf("normalize empty = %q, want %q", got, model.JobTypeRenderSegment)
	}
	if EmptyJobTypeNormalizedTotal() != before+1 {
		t.Fatalf("counter did not advance: before=%d after=%d", before, EmptyJobTypeNormalizedTotal())
	}
	for _, known := range []string{
		model.JobTypeRenderSegment,
		model.JobTypeOverlayPrepare,
		model.JobTypeOverlayRender,
		model.JobTypeOverlayImport,
	} {
		if _, err := normalizeJobType(known); err != nil {
			t.Fatalf("normalize %q: %v", known, err)
		}
	}
	if _, err := normalizeJobType("nope"); err == nil {
		t.Fatal("normalize unknown type: expected error, got nil")
	}
}
