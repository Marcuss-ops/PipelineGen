package gencore

import (
	"context"
	"errors"
	"strings"
	"testing"

	processor "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters/processor"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

type fakeQualityGate struct {
	calls   int
	quality *scriptpkg.GenerationQuality
	err     error
}

func (f *fakeQualityGate) EvaluateQualityGate(
	_ context.Context,
	_ *scriptpkg.GenerationResult,
	_ scriptpkg.GenerationItemV2,
	_ scriptpkg.ResolvedGenerationPlan,
) (*scriptpkg.GenerationQuality, error) {
	f.calls++
	return f.quality, f.err
}

// passingTestGate is the injected passing evaluator for finalizer tests that
// are not about the gate itself. Production wires the Rust adapter; the unit
// tests only need a verdict, and a missing one is now a configuration error.
func passingTestGate() *fakeQualityGate {
	return &fakeQualityGate{quality: &scriptpkg.GenerationQuality{Passed: true}}
}

func newGatedFinalizer() *GenerationFinalizer {
	finalizer := NewGenerationFinalizer(zap.NewNop(), processor.NormalizationConfig{})
	finalizer.SetQualityGate(passingTestGate())
	return finalizer
}

func finalizerFixture() (scriptpkg.GenerationItemV2, scriptpkg.ResolvedGenerationPlan, *EngineResult) {
	item := scriptpkg.GenerationItemV2{ID: "finalize-injected"}
	plan := scriptpkg.ResolvedGenerationPlan{
		ID:          "finalize-injected",
		Title:       "Test",
		Language:    "en",
		SourceText:  "the quick brown fox jumps over the lazy dog",
		TargetWords: 9,
	}
	engineResult := &EngineResult{
		Output: scriptpkg.ModelScriptOutputV1{
			Text: "the quick brown fox jumps over the lazy dog",
		},
		Model:       "test-model",
		CacheStatus: "generated",
	}
	return item, plan, engineResult
}

func TestGenerationFinalizerUsesInjectedQualityGate(t *testing.T) {
	finalizer := NewGenerationFinalizer(zap.NewNop(), processor.NormalizationConfig{})
	gate := &fakeQualityGate{quality: &scriptpkg.GenerationQuality{Passed: true, LanguageRequested: "en"}}
	finalizer.SetQualityGate(gate)

	item, plan, engineResult := finalizerFixture()
	result, err := finalizer.Finalize(context.Background(), FinalizeInputs{
		Item:         item,
		Plan:         plan,
		EngineResult: engineResult,
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gate.calls != 1 {
		t.Fatalf("injected gate calls = %d, want 1", gate.calls)
	}
	if result.Quality != gate.quality {
		t.Fatalf("result quality = %+v, want injected gate quality", result.Quality)
	}
}

// A missing evaluator is a configuration error, never a silent skip: the gate
// is Rust-owned and the composition root always wires it.
func TestGenerationFinalizerFailsClosedWithoutQualityGate(t *testing.T) {
	finalizer := NewGenerationFinalizer(zap.NewNop(), processor.NormalizationConfig{})

	item, plan, engineResult := finalizerFixture()
	_, err := finalizer.Finalize(context.Background(), FinalizeInputs{
		Item:         item,
		Plan:         plan,
		EngineResult: engineResult,
	}, nil)
	if err == nil {
		t.Fatal("expected a configuration error when the quality gate is missing")
	}
	if !strings.Contains(err.Error(), "quality gate evaluator is not configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGenerationFinalizerInjectedGateFailureIsTyped(t *testing.T) {
	finalizer := NewGenerationFinalizer(zap.NewNop(), processor.NormalizationConfig{})
	gate := &fakeQualityGate{
		quality: &scriptpkg.GenerationQuality{Passed: false, LanguageRequested: "en"},
		err: &scriptpkg.QualityGateError{
			ItemID:  "finalize-injected",
			Reasons: []string{"injected rejection"},
		},
	}
	finalizer.SetQualityGate(gate)

	item, plan, engineResult := finalizerFixture()
	result, err := finalizer.Finalize(context.Background(), FinalizeInputs{
		Item:         item,
		Plan:         plan,
		EngineResult: engineResult,
	}, nil)
	if err == nil {
		t.Fatal("expected quality gate failure")
	}
	var gateErr *scriptpkg.QualityGateError
	if !errors.As(err, &gateErr) {
		t.Fatalf("expected *QualityGateError, got %T", err)
	}
	if result == nil || result.Status != scriptpkg.ItemStatusFailed {
		t.Fatalf("expected failed result, got %+v", result)
	}
}
