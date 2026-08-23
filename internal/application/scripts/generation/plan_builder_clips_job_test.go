package generation

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestBuildPlan_ClipsRunsTranslationVoiceoverInSameJob(t *testing.T) {
	plan := BuildPlan(scriptpkg.GenerationItemV2{
		Source: scriptpkg.SourceSpec{
			Type:    scriptpkg.SourceClips,
			ClipIDs: []string{"clip-1"},
		},
		Output: scriptpkg.OutputSpec{
			TranslateTo:      "it",
			SaveToDB:         true,
			VoiceoverEnabled: scriptpkg.ToggleEnabled,
		},
	})

	want := []string{
		string(adapters.ProcessorTranslation),
		string(adapters.ProcessorClipBindings),
		string(adapters.ProcessorVoiceover),
		string(adapters.ProcessorAssetLocationReconciliation),
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

func TestBuildPlan_PreservesScriptParamsGuidelinesInPrompt(t *testing.T) {
	guidelines := "Ogni segmento tratta esclusivamente il soggetto indicato e non anticipa il successivo."
	plan := BuildPlan(scriptpkg.GenerationItemV2{
		Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceText, Topic: "Cinque pugili"},
		ScriptParams: scriptpkg.ScriptSpec{
			Guidelines: guidelines,
			Segments: []scriptpkg.ScriptSegment{{
				ID: "boxer-mike-tyson", Topic: "Mike Tyson", SourceText: "Testo autonomo.", TargetWords: 150,
			}},
		},
	})
	if plan.Guidelines != guidelines {
		t.Fatalf("plan guidelines = %q, want %q", plan.Guidelines, guidelines)
	}
	if !strings.Contains(plan.RenderedPrompt, guidelines) {
		t.Fatalf("rendered prompt omitted script_params.guidelines: %q", plan.RenderedPrompt)
	}
}

func TestBuildPlan_ClipOnlySkipsVoiceoverSideEffect(t *testing.T) {
	plan := BuildPlan(scriptpkg.GenerationItemV2{
		MediaMode: scriptpkg.MediaModeClipOnly,
		Source: scriptpkg.SourceSpec{
			Type:    scriptpkg.SourceClips,
			ClipIDs: []string{"clip-1"},
		},
		Output: scriptpkg.OutputSpec{SaveToDB: true},
	})

	for _, processor := range plan.Postprocessors {
		if processor == string(adapters.ProcessorVoiceover) {
			t.Fatalf("clip_only plan unexpectedly contains %q: %v", processor, plan.Postprocessors)
		}
	}
}

func TestBuildPlan_ReconciliationRunsBeforePersistenceAndDocument(t *testing.T) {
	plan := BuildPlan(scriptpkg.GenerationItemV2{
		Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceText, Topic: "topic"},
		Docs:   scriptpkg.DocumentsSpec{Enabled: true},
		Output: scriptpkg.OutputSpec{SaveToDB: true, VoiceoverEnabled: scriptpkg.ToggleEnabled, VoiceoverFolderID: "voiceover-folder"},
	})

	want := []string{
		string(adapters.ProcessorClipBindings),
		string(adapters.ProcessorVoiceover),
		string(adapters.ProcessorAssetLocationReconciliation),
		string(adapters.ProcessorPersistence),
		string(adapters.ProcessorDocument),
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

func TestBuildPlan_ReconciliationIsPresentOnceWithMediaAndImages(t *testing.T) {
	plan := BuildPlan(scriptpkg.GenerationItemV2{
		Source:    scriptpkg.SourceSpec{Type: scriptpkg.SourceText, Topic: "topic"},
		MediaPlan: media.MediaPlanSpec{Mode: "hybrid"},
		Output: scriptpkg.OutputSpec{
			GenerateSceneImages: scriptpkg.ToggleEnabled,
			VoiceoverFolderID:   "voiceover-folder",
			SaveToDB:            true,
		},
	})

	count := 0
	gateIndex := -1
	for i, processor := range plan.Postprocessors {
		if processor == string(adapters.ProcessorAssetLocationReconciliation) {
			count++
			gateIndex = i
		}
	}
	if count != 1 {
		t.Fatalf("reconciliation must appear exactly once, got %d in %v", count, plan.Postprocessors)
	}
	for i, processor := range plan.Postprocessors {
		if processor == string(adapters.ProcessorPersistence) || processor == string(adapters.ProcessorDocument) {
			if i <= gateIndex {
				t.Fatalf("terminal processor %q appears before reconciliation: %v", processor, plan.Postprocessors)
			}
		}
	}
	for i, processor := range plan.Postprocessors {
		if processor == string(adapters.ProcessorImages) || processor == string(adapters.ProcessorVisualPlanning) || processor == string(adapters.ProcessorVisualSlots) || processor == string(adapters.ProcessorVoiceover) {
			if i >= gateIndex {
				t.Fatalf("binding producer %q appears after reconciliation: %v", processor, plan.Postprocessors)
			}
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

func TestBuildPlan_ExplicitSceneImagesAddsImageProcessor(t *testing.T) {
	plan := BuildPlan(scriptpkg.GenerationItemV2{
		Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceText, Topic: "topic"},
		Output: scriptpkg.OutputSpec{GenerateSceneImages: scriptpkg.ToggleEnabled},
	})
	for i, processor := range plan.Postprocessors {
		if processor != string(adapters.ProcessorImages) {
			continue
		}
		if i == 0 || plan.Postprocessors[i-1] != string(adapters.ProcessorClipBindings) {
			t.Fatalf("images must follow clip_bindings; got %v", plan.Postprocessors)
		}
		return
	}
	t.Fatalf("explicit generate_scene_images missing from plan: %v", plan.Postprocessors)
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
