package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"go.uber.org/zap"
)

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
	if result.Status != "FAILED_QUALITY_GATE" {
		t.Errorf("expected status FAILED_QUALITY_GATE, got %q", result.Status)
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
