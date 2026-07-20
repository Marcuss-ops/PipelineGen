package usecase

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestBuildPlan_ClipsRunsTranslationVoiceoverDocumentInSameJob(t *testing.T) {
	plan := BuildPlan(scriptpkg.GenerationItemV2{
		Source: scriptpkg.SourceSpec{
			Type:    scriptpkg.SourceClips,
			ClipIDs: []string{"clip-1"},
		},
		Output: scriptpkg.OutputSpec{
			TranslateTo: "it",
			SaveToDB:    true,
		},
	})

	want := []string{
		string(adapters.ProcessorTranslation),
		string(adapters.ProcessorClipBindings),
		string(adapters.ProcessorStockAssociation),
		string(adapters.ProcessorVoiceover),
		string(adapters.ProcessorPersistence),
	}
	if len(plan.Postprocessors) != len(want) {
		t.Fatalf("postprocessors=%v, want %v", plan.Postprocessors, want)
	}
	for i := range want {
		if plan.Postprocessors[i] != want[i] {
			t.Fatalf("postprocessors[%d]=%q, want %q; full=%v", i, plan.Postprocessors[i], want[i], plan.Postprocessors)
		}
	}
}

func TestBuildPlan_TextDoesNotImplicitlyCreateClipArtifacts(t *testing.T) {
	plan := BuildPlan(scriptpkg.GenerationItemV2{
		Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceText, Topic: "topic"},
	})
	for _, processor := range plan.Postprocessors {
		if processor == string(adapters.ProcessorVoiceover) {
			t.Fatalf("text-only plan unexpectedly contains %q: %v", processor, plan.Postprocessors)
		}
	}
}
