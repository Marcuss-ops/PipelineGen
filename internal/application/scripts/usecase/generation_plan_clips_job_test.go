package usecase

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
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

func TestBuildPlan_TextWithoutMediaPlanSkipsVisualPlanning(t *testing.T) {
	plan := BuildPlan(scriptpkg.GenerationItemV2{Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceText, Topic: "topic"}})
	for _, processor := range plan.Postprocessors {
		if processor == string(adapters.ProcessorVisualPlanning) {
			t.Fatalf("text-only plan unexpectedly contains visual planning: %v", plan.Postprocessors)
		}
	}
}

func TestBuildPlan_MediaPlanIncludesVisualPlanningAfterClipBindings(t *testing.T) {
	plan := BuildPlan(scriptpkg.GenerationItemV2{
		Source:    scriptpkg.SourceSpec{Type: scriptpkg.SourceText, Topic: "topic"},
		MediaPlan: media.MediaPlanSpec{Mode: "hybrid"},
	})
	found := false
	for i, processor := range plan.Postprocessors {
		if processor != string(adapters.ProcessorVisualPlanning) {
			continue
		}
		found = true
		if i == 0 || plan.Postprocessors[i-1] != string(adapters.ProcessorClipBindings) {
			t.Fatalf("visual planning must immediately follow clip_bindings; got %v", plan.Postprocessors)
		}
	}
	if !found {
		t.Fatalf("plan with media plan missing visual planning: %v", plan.Postprocessors)
	}
}

func TestBuildPlan_DisabledMediaPlanSkipsVisualPlanning(t *testing.T) {
	plan := BuildPlan(scriptpkg.GenerationItemV2{
		Source:    scriptpkg.SourceSpec{Type: scriptpkg.SourceText, Topic: "topic"},
		MediaPlan: media.MediaPlanSpec{Mode: "disabled"},
	})
	for _, processor := range plan.Postprocessors {
		if processor == string(adapters.ProcessorVisualPlanning) {
			t.Fatalf("disabled media plan unexpectedly contains visual planning: %v", plan.Postprocessors)
		}
	}
}

func TestBuildPlan_EmptyMediaPlanModeSkipsVisualPlanning(t *testing.T) {
	plan := BuildPlan(scriptpkg.GenerationItemV2{
		Source:    scriptpkg.SourceSpec{Type: scriptpkg.SourceText, Topic: "topic"},
		MediaPlan: media.MediaPlanSpec{},
	})
	for _, processor := range plan.Postprocessors {
		if processor == string(adapters.ProcessorVisualPlanning) {
			t.Fatalf("empty media plan unexpectedly contains visual planning: %v", plan.Postprocessors)
		}
	}
}

func TestBuildPlan_InvalidMediaPlanModeSkipsVisualPlanning(t *testing.T) {
	plan := BuildPlan(scriptpkg.GenerationItemV2{
		Source:    scriptpkg.SourceSpec{Type: scriptpkg.SourceText, Topic: "topic"},
		MediaPlan: media.MediaPlanSpec{Mode: "bogus"},
	})
	for _, processor := range plan.Postprocessors {
		if processor == string(adapters.ProcessorVisualPlanning) {
			t.Fatalf("invalid media plan mode unexpectedly contains visual planning: %v", plan.Postprocessors)
		}
	}
}
