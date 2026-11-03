package gencore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// batchScriptedGenerator records every provider call (prompt + assignment)
// and lets the test script the output per call text. Thread-safe: batch jobs
// fan out across workers.
type batchScriptedGenerator struct {
	mu    sync.Mutex
	calls []string
	fn    func(callText string) string
}

func (g *batchScriptedGenerator) GenerateScript(_ context.Context, req scriptports.TextGenerationRequest) (*scriptports.GenerationResult, error) {
	g.mu.Lock()
	callText := req.Prompt + "||" + req.SegmentAssignment
	g.calls = append(g.calls, callText)
	g.mu.Unlock()
	return &scriptports.GenerationResult{Script: g.fn(callText), Model: "test-model"}, nil
}

func (g *batchScriptedGenerator) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.calls)
}

// batchValidationPlanFn builds a plan whose scenes are all valid prose targets
// (12 words) so per-scene QA passes once the paragraph count matches. With 20
// segments and maxSegmentGenerationCalls=10 the batches are pairs, which
// reproduces the live "scene batch N-N+1 expected 2 paragraphs" shape.
func batchValidationPlan() *scriptpkg.ResolvedGenerationPlan {
	segments := make([]scriptpkg.ScriptSegment, 0, 20)
	for i := 1; i <= 20; i++ {
		segments = append(segments, scriptpkg.ScriptSegment{
			ID:          fmt.Sprintf("s%02d", i),
			Topic:       fmt.Sprintf("s%02d", i),
			TargetWords: 12,
		})
	}
	return &scriptpkg.ResolvedGenerationPlan{
		Title:       "batch retry",
		Topic:       "batch retry",
		Language:    "en",
		Mode:        "text",
		TargetWords: 240,
		Segments:    segments,
	}
}

const batchRepairMarker = "Regenerate only this batch"

// TestEngineGenerate_BatchMismatchGetsBoundedRepair pins the fix for the live
// dead-letter failure "scene batch 4-5 expected 2 paragraphs, got 4": the
// first malformed batch must be regenerated once (1+repair policy) and the
// generation must then succeed; the other batches are unaffected.
func TestEngineGenerate_BatchMismatchGetsBoundedRepair(t *testing.T) {
	called := 0
	gen := &batchScriptedGenerator{fn: func(callText string) string {
		if strings.Contains(callText, "Topic: s01") {
			called++
			if !strings.Contains(callText, batchRepairMarker) && called == 1 {
				return strings.Join([]string{textOfNWords(6), textOfNWords(6), textOfNWords(6), textOfNWords(6)}, "\n\n")
			}
			return textOfNWords(12) + "\n\n" + textOfNWords(12)
		}
		return textOfNWords(12) + "\n\n" + textOfNWords(12)
	}}
	engine := &Engine{ollamaGen: gen, log: nil}
	engine.ConfigureSegmentValidation(15, 10, 1)

	result, err := engine.Generate(context.Background(), batchValidationPlan())
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	paragraphs := splitGeneratedBatchParagraphs(result.Output.Text)
	if len(paragraphs) != 20 {
		t.Fatalf("script paragraphs = %d, want 20; script=%q", len(paragraphs), result.Output.Text)
	}
	calls := gen.callCount()
	if calls != 11 {
		t.Fatalf("provider calls = %d, want 11 (10 batches + 1 bounded repair)", calls)
	}
}

// TestEngineGenerate_R20MinutiShapeRejectsA26SceneAnswer pins the 2026-11-03
// regression: the r20minuti payload declared FIVE compact segments (~520 words
// each) and the model answered with 26 short paragraphs. A 5-segment plan fans
// out one request per segment, and each request must yield exactly ONE
// paragraph: a 26-paragraph answer cannot be smuggled into 26 micro-scenes.
// The typed failure is what keeps the measured 26-scene cost driver out of the
// job instead of silently shipping it.
func TestEngineGenerate_R20MinutiShapeRejectsA26SceneAnswer(t *testing.T) {
	segments := make([]scriptpkg.ScriptSegment, 0, 5)
	for i := 1; i <= 5; i++ {
		segments = append(segments, scriptpkg.ScriptSegment{
			ID:          fmt.Sprintf("scene-%02d", i),
			TargetWords: 520,
		})
	}
	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:       "r20minuti",
		Topic:       "r20minuti",
		Language:    "it",
		Mode:        "text",
		TargetWords: 2600,
		Segments:    segments,
	}
	microScenes := make([]string, 0, 26)
	for i := 0; i < 26; i++ {
		microScenes = append(microScenes, textOfNWords(8))
	}
	gen := &batchScriptedGenerator{fn: func(string) string { return strings.Join(microScenes, "\n\n") }}
	engine := &Engine{ollamaGen: gen, log: nil}
	engine.ConfigureSegmentValidation(15, 10, 1)

	_, err := engine.Generate(context.Background(), plan)
	if err == nil || !errors.Is(err, scriptpkg.ErrSegmentValidationFailed) {
		t.Fatalf("a 26-paragraph answer to a 5-segment plan must fail closed, got %v", err)
	}
	if !strings.Contains(err.Error(), "expected 1 paragraphs, got 26") {
		t.Fatalf("failure must keep the one-paragraph-per-segment contract, got %v", err)
	}
	if gen.callCount() != 10 {
		t.Fatalf("provider calls = %d, want 10 (5 segments, one bounded repair each)", gen.callCount())
	}
}

// TestEngineGenerate_BatchMismatchStopsAtRepairLimit pins the bounded side of
// the policy: a batch that never splits into the right paragraph count stops
// after the configured repair budget and surfaces ErrSegmentValidationFailed.
func TestEngineGenerate_BatchMismatchStopsAtRepairLimit(t *testing.T) {
	gen := &batchScriptedGenerator{fn: func(callText string) string {
		if strings.Contains(callText, "Topic: s01") {
			return strings.Join([]string{textOfNWords(6), textOfNWords(6), textOfNWords(6), textOfNWords(6)}, "\n\n")
		}
		return textOfNWords(12) + "\n\n" + textOfNWords(12)
	}}
	engine := &Engine{ollamaGen: gen, log: nil}
	engine.ConfigureSegmentValidation(15, 10, 1)

	_, err := engine.Generate(context.Background(), batchValidationPlan())
	if err == nil || !errors.Is(err, scriptpkg.ErrSegmentValidationFailed) {
		t.Fatalf("expected typed segment validation failure, got %v", err)
	}
	if !strings.Contains(err.Error(), "expected 2 paragraphs, got 4") {
		t.Fatalf("failure must keep the expected/got contract, got %v", err)
	}
	calls := gen.callCount()
	if calls != 11 {
		t.Fatalf("provider calls = %d, want 11 (10 batches + 1 bounded repair, then stop)", calls)
	}
}
