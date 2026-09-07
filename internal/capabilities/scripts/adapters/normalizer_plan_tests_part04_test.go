package adapters_test

import (
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"testing"
)

func TestResolvedGenerationPlanHasClips(t *testing.T) {
	// Nil evidence → false.
	plan := scriptpkg.ResolvedGenerationPlan{ClipEvidence: nil}
	if plan.HasClips() {
		t.Error("nil ClipEvidence should return false")
	}

	// Empty clip IDs → false.
	plan = scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{AcceptedClipIDs: []string{}},
	}
	if plan.HasClips() {
		t.Error("empty ClipIDs should return false")
	}

	// Populated clip IDs → true.
	plan = scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{AcceptedClipIDs: []string{"clip-a"}},
	}
	if !plan.HasClips() {
		t.Error("populated ClipEvidence should return true")
	}
}

func TestResolvedGenerationPlanHasPostprocessor(t *testing.T) {
	plan := scriptpkg.ResolvedGenerationPlan{
		Postprocessors: []string{"entities", "images", "persistence"},
	}

	if !plan.HasPostprocessor("entities") {
		t.Error("should have 'entities' postprocessor")
	}
	if !plan.HasPostprocessor("images") {
		t.Error("should have 'metadata' postprocessor")
	}
	if plan.HasPostprocessor("voiceover") {
		t.Error("should NOT have 'voiceover' postprocessor")
	}
	if plan.HasPostprocessor("") {
		t.Error("empty string should not match")
	}
}

func TestResolvedGenerationPlanHasPostprocessorEmpty(t *testing.T) {
	plan := scriptpkg.ResolvedGenerationPlan{Postprocessors: nil}
	if plan.HasPostprocessor("anything") {
		t.Error("nil postprocessors should return false for any name")
	}

	plan.Postprocessors = []string{}
	if plan.HasPostprocessor("anything") {
		t.Error("empty postprocessors should return false for any name")
	}
}
