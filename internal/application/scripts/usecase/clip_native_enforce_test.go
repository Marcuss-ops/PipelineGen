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
	// Sprint 1.3 (godlike/08): enforceClipNativeContract no longer
	// writes result.Status. The orchestrator's classify phase is the
	// SOLE writer. Tests that assert the post-classify state must
	// invoke ClassifyGenerationStatus explicitly to mirror the
	// production Finalize() flow.
	result.Status = ClassifyGenerationStatus(result, false)
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

func TestEnforceClipNativeContract_ClipEvidenceBuildsScenesWithoutFallback(t *testing.T) {
	plan := clipNativePlan(scriptpkg.FallbackPolicyAllowProse, []string{"c1"})
	engineResult := engineResultWithScenes([]string{}) // no scenes from model
	postResult := &adapters.PipelineResult{
		SynthesizedScenes: []scriptpkg.SpecScene{{ID: "scene-c1", Index: 0, Text: "clip evidence", Bindings: scriptpkg.SceneBindings{Clip: &scriptpkg.ClipBinding{ClipID: "c1"}}}},
		FinalSpecScene:    scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{ID: "scene-c1", Index: 0, Text: "clip evidence", Bindings: scriptpkg.SceneBindings{Clip: &scriptpkg.ClipBinding{ClipID: "c1"}}}}},
	}

	result := &scriptpkg.GenerationResult{}
	if err := enforceClipNativeContract(result, clipNativeItem(), plan, engineResult, postResult); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// Sprint 1.3 (godlike/08): see note above.
	result.Status = ClassifyGenerationStatus(result, false)
	if result.Status != scriptpkg.ItemStatusSucceeded {
		t.Errorf("expected status %s, got %q", scriptpkg.ItemStatusSucceeded, result.Status)
	}
	if result.ModeInfo == nil || result.ModeInfo.FallbackUsed || result.ModeInfo.UsedMode != "clip_native" {
		t.Errorf("expected no fallback mode info, got %+v", result.ModeInfo)
	}
}

func TestEnforceClipNativeContract_NoClipEvidenceFailsWithPlanUnavailable(t *testing.T) {
	plan := clipNativePlan(scriptpkg.FallbackPolicyStrict, []string{})
	plan.ClipEvidence = nil
	engineResult := engineResultWithScenes([]string{}) // no scenes
	postResult := &adapters.PipelineResult{}

	result := &scriptpkg.GenerationResult{}
	err := enforceClipNativeContract(result, clipNativeItem(), plan, engineResult, postResult)
	if err == nil {
		t.Fatal("expected error when no clip evidence is available")
	}
	if !scriptpkg.IsClipNativePlanningFailed(err) {
		t.Errorf("expected ClipNativePlanningFailed, got %T", err)
	}
	if !strings.Contains(err.Error(), "CLIP_NATIVE_PLAN_UNAVAILABLE") {
		t.Errorf("expected error to contain CLIP_NATIVE_PLAN_UNAVAILABLE, got %v", err)
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
	// Sprint 1.3 (godlike/08): see note above. allow_prose with
	// a fallback warning produces SUCCEEDED_WITH_WARNINGS after the
	// central classify phase (warnings were appended by enforce).
	result.Status = ClassifyGenerationStatus(result, false)
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
