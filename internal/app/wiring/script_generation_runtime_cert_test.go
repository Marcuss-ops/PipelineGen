package wiring

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestBuildRuntimeMediaCertSpecDoesNotInventTextSceneIdentity(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{Mode: "text"}
	spec := buildRuntimeMediaCertSpec(plan)
	if spec.Segments != 0 {
		t.Fatalf("segments = %d, want 0 without an authored segment contract", spec.Segments)
	}
	if len(spec.SegmentsExpected) != 0 {
		t.Fatalf("segments_expected = %+v, want none for free-form generated scenes", spec.SegmentsExpected)
	}
}

func TestBuildRuntimeMediaCertSpecUsesExplicitIDsAndDeterministicFallback(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		Mode: "text",
		Segments: []scriptpkg.ScriptSegment{
			{ID: "authored-a", Topic: "Donald Trump"},
			{Topic: "United States"},
		},
	}
	spec := buildRuntimeMediaCertSpec(plan)
	if len(spec.SegmentsExpected) != 2 {
		t.Fatalf("segments_expected = %d, want 2", len(spec.SegmentsExpected))
	}
	if spec.SegmentsExpected[0].ID != "authored-a" || spec.SegmentsExpected[1].ID != "scene-1" {
		t.Fatalf("unexpected explicit identity contract: %+v", spec.SegmentsExpected)
	}
}
