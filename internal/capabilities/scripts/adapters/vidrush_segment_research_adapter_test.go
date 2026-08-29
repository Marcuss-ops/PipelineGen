package adapters

import (
	"context"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestVidRushSegmentResearchAdapterNilResolverIsSafe(t *testing.T) {
	adapter := NewVidRushSegmentResearchAdapter(nil)
	report, err := adapter.ResearchSegment(context.Background(), &scriptpkg.ResolvedGenerationPlan{Language: "en"}, scriptpkg.VidRushSegmentResult{SegmentID: "segment-1", Text: "topic"})
	if err != nil {
		t.Fatal(err)
	}
	if report != nil {
		t.Fatalf("report=%+v, want nil when resolver is unavailable", report)
	}
}

func TestVidRushSegmentResearchAdapterEmptySegmentIsNoop(t *testing.T) {
	adapter := NewVidRushSegmentResearchAdapter(nil)
	report, err := adapter.ResearchSegment(context.Background(), nil, scriptpkg.VidRushSegmentResult{SegmentID: "segment-1"})
	if err != nil {
		t.Fatal(err)
	}
	if report != nil {
		t.Fatalf("report=%+v, want nil for empty segment", report)
	}
}
