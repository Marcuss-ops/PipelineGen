package usecase

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestEnforceClipNativeContract_StrictSuccess(t *testing.T) {
	plan := clipNativePlan(scriptpkg.FallbackPolicyStrict, []string{"c1", "c2"})
	engineResult := engineResultWithScenes([]string{"c1", "c2"})
	postResult := &adapters.PipelineResult{FinalSpecScene: engineResult.Output.SpecScene}

	result := &scriptpkg.GenerationResult{}
	if err := enforceClipNativeContract(result, clipNativeItem(), plan, engineResult, postResult); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Status != scriptpkg.ItemStatusSucceeded {
		t.Errorf("expected status %s, got %q", scriptpkg.ItemStatusSucceeded, result.Status)
	}
	if result.ModeInfo == nil || result.ModeInfo.FallbackUsed {
		t.Errorf("expected no fallback, got %+v", result.ModeInfo)
	}
}

func TestEnforceClipNativeContract_StrictProseFallbackFails(t *testing.T) {
	plan := clipNativePlan(scriptpkg.FallbackPolicyStrict, []string{"c1"})
	engineResult := engineResultWithScenes([]string{}) // no scenes
	postResult := &adapters.PipelineResult{
		SynthesizedScenes: []scriptpkg.SpecScene{{ID: "s1", Index: 0, Text: "fallback"}},
		FinalSpecScene:    scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{ID: "s1", Index: 0, Text: "fallback"}}},
	}

	result := &scriptpkg.GenerationResult{}
	err := enforceClipNativeContract(result, clipNativeItem(), plan, engineResult, postResult)
	if err == nil {
		t.Fatal("expected error for strict prose fallback")
	}
	if !scriptpkg.IsClipNativePlanningFailed(err) {
		t.Errorf("expected ClipNativePlanningFailed, got %T", err)
	}
}

func TestEnforceClipNativeContract_StrictSceneCountMismatchFails(t *testing.T) {
	plan := clipNativePlan(scriptpkg.FallbackPolicyStrict, []string{"c1", "c2"})
	engineResult := engineResultWithScenes([]string{"c1"}) // only one scene
	postResult := &adapters.PipelineResult{FinalSpecScene: engineResult.Output.SpecScene}

	result := &scriptpkg.GenerationResult{}
	err := enforceClipNativeContract(result, clipNativeItem(), plan, engineResult, postResult)
	if err == nil {
		t.Fatal("expected error for scene count mismatch")
	}
}

func TestEnforceClipNativeContract_StrictMissingBindingFails(t *testing.T) {
	plan := clipNativePlan(scriptpkg.FallbackPolicyStrict, []string{"c1", "c2"})
	engineResult := engineResultWithScenes([]string{"c1", "c2"})
	// Clear one binding to simulate a missing association.
	engineResult.Output.SpecScene.Scenes[1].Bindings.Clip = nil
	postResult := &adapters.PipelineResult{FinalSpecScene: engineResult.Output.SpecScene}

	result := &scriptpkg.GenerationResult{}
	err := enforceClipNativeContract(result, clipNativeItem(), plan, engineResult, postResult)
	if err == nil {
		t.Fatal("expected error for missing clip binding")
	}
}

func TestEnforceClipNativeContract_AllowProseFallbackSucceedsWithWarnings(t *testing.T) {
	plan := clipNativePlan(scriptpkg.FallbackPolicyAllowProse, []string{"c1"})
	engineResult := engineResultWithScenes([]string{}) // no scenes
	postResult := &adapters.PipelineResult{
		SynthesizedScenes: []scriptpkg.SpecScene{{ID: "s1", Index: 0, Text: "fallback"}},
		FinalSpecScene:    scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{ID: "s1", Index: 0, Text: "fallback"}}},
	}

	result := &scriptpkg.GenerationResult{}
	if err := enforceClipNativeContract(result, clipNativeItem(), plan, engineResult, postResult); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Status != scriptpkg.ItemStatusSucceededWithWarnings {
		t.Errorf("expected status %s, got %q", scriptpkg.ItemStatusSucceededWithWarnings, result.Status)
	}
	if result.ModeInfo == nil || !result.ModeInfo.FallbackUsed || result.ModeInfo.UsedMode != "prose" {
		t.Errorf("expected prose fallback mode info, got %+v", result.ModeInfo)
	}
	if len(result.Warnings) == 0 {
		t.Error("expected warnings")
	}
	foundCode := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "CLIP_NATIVE_PLAN_UNAVAILABLE") {
			foundCode = true
			break
		}
	}
	if !foundCode {
		t.Errorf("expected warning with CLIP_NATIVE_PLAN_UNAVAILABLE, got %v", result.Warnings)
	}
}

func TestEnforceClipNativeContract_AllowProseMismatchSucceedsWithWarnings(t *testing.T) {
	plan := clipNativePlan(scriptpkg.FallbackPolicyAllowProse, []string{"c1", "c2"})
	engineResult := engineResultWithScenes([]string{"c1"}) // missing one scene
	postResult := &adapters.PipelineResult{FinalSpecScene: engineResult.Output.SpecScene}

	result := &scriptpkg.GenerationResult{}
	if err := enforceClipNativeContract(result, clipNativeItem(), plan, engineResult, postResult); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Status != scriptpkg.ItemStatusSucceededWithWarnings {
		t.Errorf("expected status %s, got %q", scriptpkg.ItemStatusSucceededWithWarnings, result.Status)
	}
}

// helpers

func clipNativeItem() scriptpkg.GenerationItemV2 {
	return scriptpkg.GenerationItemV2{ID: "item-1"}
}

func clipNativePlan(policy string, clipIDs []string) scriptpkg.ResolvedGenerationPlan {
	return scriptpkg.ResolvedGenerationPlan{
		SourceKind:     string(scriptpkg.SourceClips),
		FallbackPolicy: policy,
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: clipIDs,
			DriveLinks:      map[string]string{"c1": "https://drive/c1", "c2": "https://drive/c2"},
		},
	}
}

func engineResultWithScenes(clipIDs []string) *EngineResult {
	scenes := make([]scriptpkg.SpecScene, len(clipIDs))
	for i, id := range clipIDs {
		scenes[i] = scriptpkg.SpecScene{
			ID:    "scene-" + id,
			Index: i,
			Text:  "scene text",
			Bindings: scriptpkg.SceneBindings{
				Clip: &scriptpkg.ClipBinding{ClipID: id},
			},
		}
	}
	return &EngineResult{
		Output: scriptpkg.ModelScriptOutputV1{
			SchemaVersion: 1,
			Text:          "script text",
			SpecScene:     scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes},
		},
		WordCount: 10,
	}
}
