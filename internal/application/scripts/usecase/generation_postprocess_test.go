package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// fakePostProcessor is a test double that returns a fixed result.
type fakePostProcessor struct {
	name   adapters.ProcessorName
	policy adapters.ProcessorPolicy
	result *adapters.PostProcessResult
	err    error
}

func (f *fakePostProcessor) Name() adapters.ProcessorName { return f.name }
func (f *fakePostProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) adapters.ProcessorPolicy {
	return f.policy
}
func (f *fakePostProcessor) Process(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, _ adapters.ProcessInput) (*adapters.PostProcessResult, error) {
	return f.result, f.err
}

func TestGenerationPostprocessor_Process_NilRegistry_ReturnsEmptyProcessedGeneration(t *testing.T) {
	post := NewGenerationPostprocessor(nil)
	item := scriptpkg.GenerationItemV2{ID: "post-nil"}
	plan := scriptpkg.ResolvedGenerationPlan{ID: "post-nil"}
	engineResult := &EngineResult{
		Output: scriptpkg.ModelScriptOutputV1{Text: "hello"},
		Model:  "test-model",
	}

	processed, err := post.Process(context.Background(), item, plan, engineResult, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if processed == nil {
		t.Fatal("expected non-nil ProcessedGeneration")
	}
	if processed.PostResult != nil {
		t.Errorf("expected nil PostResult when registry is nil, got %+v", processed.PostResult)
	}
	if processed.Provenance == nil {
		t.Error("expected non-nil Provenance")
	}
	if processed.PostprocessMs == nil {
		t.Error("expected non-empty PostprocessMs map")
	}
}

func TestGenerationPostprocessor_Process_Success(t *testing.T) {
	reg := adapters.NewPostProcessorRegistry(nil)
	reg.Register(&fakePostProcessor{
		name:   "fake",
		policy: adapters.ProcessorBestEffort,
		result: &adapters.PostProcessResult{
			DocLink: "https://docs.example.com/doc1",
			DocID:   "doc1",
		},
	})
	reg.Freeze()

	post := NewGenerationPostprocessor(reg)
	item := scriptpkg.GenerationItemV2{ID: "post-success"}
	plan := scriptpkg.ResolvedGenerationPlan{
		ID:             "post-success",
		Postprocessors: []string{"fake"},
	}
	engineResult := &EngineResult{
		Output: scriptpkg.ModelScriptOutputV1{Text: "hello world"},
		Model:  "test-model",
	}

	var events []string
	tracker := NewProgressTracker(nil, item.ID)
	tracker.SetEventFn(func(eventType, message string, data map[string]any) {
		events = append(events, eventType)
	})

	processed, err := post.Process(context.Background(), item, plan, engineResult, tracker)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if processed == nil || processed.PostResult == nil {
		t.Fatal("expected non-nil PostResult")
	}
	if processed.PostResult.DocLink != "https://docs.example.com/doc1" {
		t.Errorf("expected DocLink set, got %s", processed.PostResult.DocLink)
	}
	if processed.Provenance == nil {
		t.Error("expected non-nil Provenance")
	}
	if len(processed.PostprocessMs) == 0 {
		t.Error("expected StageDurations to be populated")
	}
	if _, ok := processed.PostprocessMs["fake"]; !ok {
		t.Errorf("expected PostprocessMs to contain 'fake', got %v", processed.PostprocessMs)
	}
}

func TestGenerationPostprocessor_Process_NilEngineResult_ReturnsTypedError(t *testing.T) {
	reg := adapters.NewPostProcessorRegistry(nil)
	reg.Register(&fakePostProcessor{
		name:   "fake",
		policy: adapters.ProcessorBestEffort,
		result: &adapters.PostProcessResult{},
	})
	reg.Freeze()

	post := NewGenerationPostprocessor(reg)
	item := scriptpkg.GenerationItemV2{ID: "post-nil-engine"}
	plan := scriptpkg.ResolvedGenerationPlan{ID: "post-nil-engine"}

	_, err := post.Process(context.Background(), item, plan, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var ppErr *scriptpkg.PostprocessError
	if !errors.As(err, &ppErr) {
		t.Fatalf("expected *PostprocessError, got %T", err)
	}
	if ppErr.Processor != "engine" {
		t.Errorf("expected Processor 'engine', got %q", ppErr.Processor)
	}
}

func TestGenerationPostprocessor_Process_EmptyPostprocessors_ReturnsEmptyResult(t *testing.T) {
	reg := adapters.NewPostProcessorRegistry(nil)
	reg.Register(&fakePostProcessor{
		name:   "fake",
		policy: adapters.ProcessorBestEffort,
		result: &adapters.PostProcessResult{DocID: "should-not-run"},
	})
	reg.Freeze()

	post := NewGenerationPostprocessor(reg)
	item := scriptpkg.GenerationItemV2{ID: "post-empty"}
	plan := scriptpkg.ResolvedGenerationPlan{ID: "post-empty"}
	engineResult := &EngineResult{
		Output: scriptpkg.ModelScriptOutputV1{Text: "hello"},
		Model:  "test-model",
	}

	processed, err := post.Process(context.Background(), item, plan, engineResult, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if processed == nil || processed.PostResult == nil {
		t.Fatal("expected non-nil PostResult")
	}
	if processed.PostResult.DocID != "" {
		t.Errorf("expected no processor to run, got DocID %q", processed.PostResult.DocID)
	}
	if len(processed.PostprocessMs) != 0 {
		t.Errorf("expected empty timings, got %v", processed.PostprocessMs)
	}
}

func TestGenerationPostprocessor_Process_ClipEvidence_EmitsClipsBoundEvent(t *testing.T) {
	reg := adapters.NewPostProcessorRegistry(nil)
	reg.Register(&fakePostProcessor{
		name:   "fake",
		policy: adapters.ProcessorBestEffort,
		result: &adapters.PostProcessResult{},
	})
	reg.Freeze()

	post := NewGenerationPostprocessor(reg)
	item := scriptpkg.GenerationItemV2{ID: "post-clips"}
	plan := scriptpkg.ResolvedGenerationPlan{
		ID:             "post-clips",
		Postprocessors: []string{"fake"},
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-1", "clip-2"},
		},
	}
	engineResult := &EngineResult{
		Output: scriptpkg.ModelScriptOutputV1{Text: "hello"},
		Model:  "test-model",
	}

	var clipBound bool
	tracker := NewProgressTracker(nil, item.ID)
	tracker.SetEventFn(func(eventType, message string, data map[string]any) {
		if eventType == "clips.bound" {
			clipBound = true
		}
	})

	_, err := post.Process(context.Background(), item, plan, engineResult, tracker)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !clipBound {
		t.Error("expected clips.bound event to be emitted")
	}
}

func TestGenerationPostprocessor_Process_RequiredFailure_ReturnsTypedError(t *testing.T) {
	reg := adapters.NewPostProcessorRegistry(nil)
	reg.Register(&fakePostProcessor{
		name:   "failing",
		policy: adapters.ProcessorRequired,
		err:    errors.New("boom"),
	})
	reg.Freeze()

	post := NewGenerationPostprocessor(reg)
	item := scriptpkg.GenerationItemV2{ID: "post-fail"}
	plan := scriptpkg.ResolvedGenerationPlan{
		ID:             "post-fail",
		Postprocessors: []string{"failing"},
	}
	engineResult := &EngineResult{
		Output: scriptpkg.ModelScriptOutputV1{Text: "hello"},
		Model:  "test-model",
	}

	_, err := post.Process(context.Background(), item, plan, engineResult, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var ppErr *scriptpkg.PostprocessError
	if !errors.As(err, &ppErr) {
		t.Fatalf("expected *PostprocessError, got %T", err)
	}
	if ppErr.ItemID != item.ID {
		t.Errorf("expected ItemID %q, got %q", item.ID, ppErr.ItemID)
	}
	if ppErr.Processor != "registry" {
		t.Errorf("expected Processor 'registry', got %q", ppErr.Processor)
	}
}
