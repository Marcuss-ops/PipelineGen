package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"go.uber.org/zap"
)

func TestGenerationFinalizer_SavesGeneratedScriptToCache(t *testing.T) {
	gate := newFakeScriptMemoryGate()
	svc := adapters.NewService(gate, zap.NewNop())
	finalizer := NewGenerationFinalizer(zap.NewNop(), adapters.NormalizationConfig{})
	finalizer.SetMemoryService(svc)

	item := scriptpkg.GenerationItemV2{ID: "finalize-cache"}
	plan := scriptpkg.ResolvedGenerationPlan{
		ID:             "finalize-cache",
		Title:          "Cached",
		Language:       "en",
		Mode:           "text",
		UseMemory:      true,
		CacheKey:       "cache-key-123",
		RenderedPrompt: "prompt",
		// Keep the fixture above the editorial coverage threshold so
		// this test observes cache persistence rather than quality-gate
		// rejection.
		SourceText: "cached script text",
	}
	engineResult := &EngineResult{
		Output: scriptpkg.ModelScriptOutputV1{
			Text:      "cached script text",
			WordCount: 42,
		},
		Model:       "test-model",
		CacheStatus: "generated",
	}
	provenance := &scriptpkg.GenerationProvenance{Model: "test-model"}

	result, err := finalizer.Finalize(context.Background(), FinalizeInputs{
		Item:         item,
		Plan:         plan,
		EngineResult: engineResult,
		Provenance:   provenance,
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Verify the row was written.
	res, err := svc.CheckGate(context.Background(), adapters.MemoryGateRequest{
		ChannelID: "default",
		Mode:      "text",
		CacheKey:  "cache-key-123",
		UseMemory: true,
	})
	if err != nil {
		t.Fatalf("CheckGate failed: %v", err)
	}
	if res == nil {
		t.Fatal("expected cache row to be saved")
	}
	if res.Output != "cached script text" {
		t.Errorf("output = %q, want %q", res.Output, "cached script text")
	}
	if res.WordCount != 42 {
		t.Errorf("word_count = %d, want 42", res.WordCount)
	}
	if res.Model != "test-model" {
		t.Errorf("model = %q, want test-model", res.Model)
	}
}

func TestGenerationFinalizer_DoesNotSaveCacheHit(t *testing.T) {
	gate := newFakeScriptMemoryGate()
	svc := adapters.NewService(gate, zap.NewNop())
	finalizer := NewGenerationFinalizer(zap.NewNop(), adapters.NormalizationConfig{})
	finalizer.SetMemoryService(svc)

	// Pre-seed the cache.
	_, err := svc.SaveAfterGeneration(context.Background(), adapters.SaveGenerationInput{
		ChannelID: "default",
		Mode:      "text",
		Language:  "en",
		Title:     "Cached",
		Prompt:    "p",
		Model:     "m",
		WordCount: 1,
		CacheKey:  "cache-key-dup",
	}, "old")
	if err != nil {
		t.Fatalf("seed cache failed: %v", err)
	}

	item := scriptpkg.GenerationItemV2{ID: "finalize-cache-hit"}
	plan := scriptpkg.ResolvedGenerationPlan{
		ID:             "finalize-cache-hit",
		Title:          "Cached",
		Language:       "en",
		Mode:           "text",
		UseMemory:      true,
		CacheKey:       "cache-key-dup",
		RenderedPrompt: "p",
		SourceText:     "old",
	}
	engineResult := &EngineResult{
		Output: scriptpkg.ModelScriptOutputV1{
			Text:      "old",
			WordCount: 99,
		},
		Model:       "test-model",
		CacheStatus: "exact_hit",
	}

	_, err = finalizer.Finalize(context.Background(), FinalizeInputs{
		Item:         item,
		Plan:         plan,
		EngineResult: engineResult,
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	res, err := svc.CheckGate(context.Background(), adapters.MemoryGateRequest{
		ChannelID: "default",
		Mode:      "text",
		CacheKey:  "cache-key-dup",
		UseMemory: true,
	})
	if err != nil {
		t.Fatalf("CheckGate failed: %v", err)
	}
	if res == nil || res.Output != "old" {
		t.Errorf("cache row should be unchanged, got %+v", res)
	}
}

func TestGenerationFinalizer_Finalize_Success(t *testing.T) {
	finalizer := NewGenerationFinalizer(zap.NewNop(), adapters.NormalizationConfig{})
	item := scriptpkg.GenerationItemV2{ID: "finalize-success"}
	plan := scriptpkg.ResolvedGenerationPlan{
		ID:         "finalize-success",
		Title:      "Test",
		Language:   "en",
		SourceText: "the quick brown fox jumps over the lazy dog",
	}
	engineResult := &EngineResult{
		Output: scriptpkg.ModelScriptOutputV1{
			Text: "the quick brown fox jumps over the lazy dog",
		},
		Model:       "test-model",
		CacheStatus: "generated",
	}
	provenance := &scriptpkg.GenerationProvenance{Model: "test-model"}

	result, err := finalizer.Finalize(context.Background(), FinalizeInputs{
		Item:         item,
		Plan:         plan,
		EngineResult: engineResult,
		Provenance:   provenance,
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Provenance != provenance {
		t.Error("expected provenance to be surfaced on result")
	}
	if result.Quality == nil {
		t.Error("expected quality to be populated")
	}
	if !result.Quality.Passed {
		t.Errorf("expected quality gate to pass, got %+v", result.Quality)
	}
}

func TestGenerationFinalizer_Finalize_ClipNativeContractFails(t *testing.T) {
	finalizer := NewGenerationFinalizer(zap.NewNop(), adapters.NormalizationConfig{})
	item := scriptpkg.GenerationItemV2{ID: "finalize-clip"}
	plan := scriptpkg.ResolvedGenerationPlan{
		ID:         "finalize-clip",
		Title:      "Test",
		Language:   "en",
		SourceKind: string(scriptpkg.SourceClips),
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-1"},
		},
	}
	engineResult := &EngineResult{
		Output: scriptpkg.ModelScriptOutputV1{
			Text:      "some text",
			SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{}},
		},
		Model:       "test-model",
		CacheStatus: "generated",
	}

	_, err := finalizer.Finalize(context.Background(), FinalizeInputs{
		Item:         item,
		Plan:         plan,
		EngineResult: engineResult,
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var clipErr *scriptpkg.ClipNativePlanningError
	if !errors.As(err, &clipErr) {
		t.Fatalf("expected *ClipNativePlanningError, got %T", err)
	}
}

func TestGenerationFinalizer_Finalize_QualityGateFails(t *testing.T) {
	finalizer := NewGenerationFinalizer(zap.NewNop(), adapters.NormalizationConfig{})
	item := scriptpkg.GenerationItemV2{ID: "finalize-quality"}
	plan := scriptpkg.ResolvedGenerationPlan{
		ID:          "finalize-quality",
		Title:       "Test",
		Language:    "en",
		SourceText:  "the quick brown fox jumps over the lazy dog",
		TargetWords: 10,
	}
	engineResult := &EngineResult{
		Output: scriptpkg.ModelScriptOutputV1{
			Text: "totally unrelated text that is far away from source and too long for target",
		},
		Model:       "test-model",
		CacheStatus: "generated",
	}

	result, err := finalizer.Finalize(context.Background(), FinalizeInputs{
		Item:         item,
		Plan:         plan,
		EngineResult: engineResult,
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var qErr *scriptpkg.QualityGateError
	if !errors.As(err, &qErr) {
		t.Fatalf("expected *QualityGateError, got %T", err)
	}
	if result == nil {
		t.Fatal("expected result to be returned on quality gate failure")
	}
	if result.Status != scriptpkg.ItemStatusFailed {
		t.Errorf("expected status %s, got %q", scriptpkg.ItemStatusFailed, result.Status)
	}
}

func TestGenerationFinalizer_Finalize_SkipQualityGate(t *testing.T) {
	finalizer := NewGenerationFinalizer(zap.NewNop(), adapters.NormalizationConfig{})
	item := scriptpkg.GenerationItemV2{ID: "finalize-skip"}
	item.ScriptParams.SkipQualityGate = true
	plan := scriptpkg.ResolvedGenerationPlan{
		ID:          "finalize-skip",
		Title:       "Test",
		Language:    "en",
		SourceText:  "the quick brown fox jumps over the lazy dog",
		TargetWords: 10,
	}
	engineResult := &EngineResult{
		Output: scriptpkg.ModelScriptOutputV1{
			Text: "totally unrelated text that is far away from source and too long for target",
		},
		Model:       "test-model",
		CacheStatus: "generated",
	}

	result, err := finalizer.Finalize(context.Background(), FinalizeInputs{
		Item:         item,
		Plan:         plan,
		EngineResult: engineResult,
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Quality == nil {
		t.Fatal("expected quality to be populated")
	}
	if result.Quality.Passed {
		t.Error("expected quality gate to fail when skipped")
	}
}

// TestClassifyGenerationStatus_CleanContractEndToEnd codifies the
// end-to-end success contract: a GenerationResult with
// Quality.Passed=true and an empty Warnings slice must classify as
// ItemStatusSucceeded (NOT ItemStatusSucceededWithWarnings) when
// qualitySkipped=false. This pins the contract that the
// postprocessor_composite_run.go SWW-suppression filter relies on:
// after best-effort warnings on the single-segment documentary
// shape are filtered out, ClassifyGenerationStatus must report
// SUCCEEDED, never SUCCEEDED_WITH_WARNINGS.
func TestClassifyGenerationStatus_CleanContractEndToEnd(t *testing.T) {
	result := &scriptpkg.GenerationResult{
		Quality:  &scriptpkg.GenerationQuality{Passed: true},
		Warnings: []string{},
	}
	if got := ClassifyGenerationStatus(result, false); got != scriptpkg.ItemStatusSucceeded {
		t.Errorf("ClassifyGenerationStatus(Quality.Passed=true, Warnings=[], qualitySkipped=false) = %q, want %q",
			got, scriptpkg.ItemStatusSucceeded)
	}
}

// TestClassifyGenerationStatus_BindingWarningsFilteredOnTextPlan
// codifies the second-clause contract of the postprocessor SWW
// gate. When the plan is SingleScene=true, SourceKind="text", and
// postprocessor_composite_run.go has dropped the "clip_search: ...
// " soft-warnings (because a Stock binding populated in the
// post-walk SpecScene — Stock.DriveLink != "" or Stock.AssetID !=
// "" — confirms downstream binding presence), the resulting
// Warnings slice is empty and ClassifyGenerationStatus must report
// ItemStatusSucceeded, NOT ItemStatusSucceededWithWarnings.
func TestClassifyGenerationStatus_BindingWarningsFilteredOnTextPlan(t *testing.T) {
	// Simulate the post-filter state the composite produces on the
	// SingleScene + text + Stock-populated branch:
	// filterBestEffortBindingWarnings drops "clip_search: ...", leaving
	// the slice empty for this scenario.
	result := &scriptpkg.GenerationResult{
		Quality:  &scriptpkg.GenerationQuality{Passed: true},
		Warnings: []string{},
	}
	if got := ClassifyGenerationStatus(result, false); got != scriptpkg.ItemStatusSucceeded {
		t.Errorf("ClassifyGenerationStatus(Quality.Passed=true, filtered clip_search warnings, qualitySkipped=false) = %q, want %q",
			got, scriptpkg.ItemStatusSucceeded)
	}
}

func TestClassifyGenerationStatus_ReconciliationWarningIsPartial(t *testing.T) {
	result := &scriptpkg.GenerationResult{
		Quality: &scriptpkg.GenerationQuality{Passed: true},
		Warnings: []string{
			"asset_location_reconciliation: clip link in scene-0 is MISSING (link cleared)",
		},
	}
	if got := ClassifyGenerationStatus(result, false); got != scriptpkg.ItemStatusPartiallySucceeded {
		t.Errorf("reconciliation warning status = %q, want %q", got, scriptpkg.ItemStatusPartiallySucceeded)
	}
}

func TestClassifyGenerationStatus_NonReconciliationWarningRemainsWarningSuccess(t *testing.T) {
	result := &scriptpkg.GenerationResult{
		Quality:  &scriptpkg.GenerationQuality{Passed: true},
		Warnings: []string{"voiceover skipped: provider unavailable"},
	}
	if got := ClassifyGenerationStatus(result, false); got != scriptpkg.ItemStatusSucceededWithWarnings {
		t.Errorf("ordinary warning status = %q, want %q", got, scriptpkg.ItemStatusSucceededWithWarnings)
	}
}
