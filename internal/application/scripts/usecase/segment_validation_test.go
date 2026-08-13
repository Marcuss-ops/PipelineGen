package usecase

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"go.uber.org/zap"
)

type sequentialSegmentGenerator struct {
	mu      sync.Mutex
	results []*scriptports.GenerationResult
	prompts []string
}

func (g *sequentialSegmentGenerator) GenerateScript(_ context.Context, req scriptports.TextGenerationRequest) (*scriptports.GenerationResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.prompts = append(g.prompts, req.Prompt)
	if len(g.results) == 0 {
		return nil, errors.New("no scripted result")
	}
	result := g.results[0]
	g.results = g.results[1:]
	return result, nil
}

func proseResult(text string) *scriptports.GenerationResult {
	return &scriptports.GenerationResult{Script: text, WordCount: len(strings.Fields(text)), Model: "test-model"}
}

func segmentPlan(target int) *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{
		Title:       "segment validation",
		Topic:       "segment validation",
		Language:    "en",
		Mode:        "text",
		TargetWords: target,
		Segments: []scriptpkg.ScriptSegment{
			{ID: "one", Topic: "one", TargetWords: target / 2},
			{ID: "two", Topic: "two", TargetWords: target / 2},
		},
	}
}

func TestValidateSegmentShape_ExplicitBounds(t *testing.T) {
	item := scriptpkg.GenerationItemV2{
		ID:     "bounds",
		Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceText, Topic: "topic"},
		ScriptParams: scriptpkg.ScriptSpec{Segments: []scriptpkg.ScriptSegment{{
			Topic: "one", TargetWords: 10, MinWords: 12, MaxWords: 9,
		}}},
	}
	details := validateScriptSegmentShape(item.ScriptParams, item.ID)
	if len(details) != 3 {
		t.Fatalf("expected explicit bound violations, got %v", details)
	}
	if !strings.Contains(strings.Join(details, " "), "min_words cannot exceed max_words") {
		t.Fatalf("missing min/max violation: %v", details)
	}
	if !strings.Contains(strings.Join(details, " "), "max_words cannot be below target_words") {
		t.Fatalf("missing target/max violation: %v", details)
	}
}

func TestValidateSegmentTexts_BoundsAndTotal(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		TargetWords: 20,
		Segments: []scriptpkg.ScriptSegment{
			{Topic: "one", TargetWords: 10, MinWords: 8, MaxWords: 12},
			{Topic: "two", TargetWords: 10, MinWords: 8, MaxWords: 12},
		},
	}
	settings := segmentValidationSettings{segmentTolerancePercent: 15, totalTolerancePercent: 10}

	valid := validateSegmentTexts(plan, []string{textOfNWords(10), textOfNWords(10)}, settings)
	if !valid.Valid || len(valid.InvalidIndexes) != 0 {
		t.Fatalf("valid segments rejected: %+v", valid)
	}
	invalid := validateSegmentTexts(plan, []string{textOfNWords(7), textOfNWords(10)}, settings)
	if invalid.Valid || len(invalid.InvalidIndexes) != 1 || invalid.InvalidIndexes[0] != 0 {
		t.Fatalf("expected only segment 0 to fail: %+v", invalid)
	}
	total := validateSegmentTexts(plan, []string{textOfNWords(12), textOfNWords(12)}, settings)
	if total.Valid || len(total.InvalidIndexes) != 1 {
		t.Fatalf("expected total failure to select one mutable segment: %+v", total)
	}
	if total.ActualTotal != 24 || total.TotalMin != 18 || total.TotalMax != 22 {
		t.Fatalf("unexpected total report: %+v", total)
	}
}

func TestEngineGenerate_SelectiveSegmentRegenerationFreezesValidText(t *testing.T) {
	gen := &sequentialSegmentGenerator{results: []*scriptports.GenerationResult{
		proseResult("short\n\n" + textOfNWords(10)),
		proseResult(textOfNWords(10)),
	}}
	engine := &Engine{ollamaGen: gen, log: zap.NewNop()}
	engine.ConfigureSegmentValidation(15, 10, 2)
	plan := segmentPlan(20)

	result, err := engine.Generate(context.Background(), plan)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	want := textOfNWords(10) + "\n\n" + textOfNWords(10)
	if result.Output.Text != want {
		t.Fatalf("frozen/merged text = %q, want %q", result.Output.Text, want)
	}
	if len(gen.prompts) != 2 {
		t.Fatalf("provider calls = %d, want 2", len(gen.prompts))
	}
	if !strings.Contains(gen.prompts[1], "FROZEN SEGMENT 2:\n"+textOfNWords(10)) {
		t.Fatalf("second prompt did not preserve valid segment exactly: %q", gen.prompts[1])
	}
	if !strings.Contains(gen.prompts[1], "Regenerate ONLY these segment numbers, in this order: 1") {
		t.Fatalf("second prompt did not target only segment 1: %q", gen.prompts[1])
	}
}

func TestEngineGenerate_SegmentRegenerationStopsAtRetryLimit(t *testing.T) {
	gen := &sequentialSegmentGenerator{results: []*scriptports.GenerationResult{
		proseResult("short\n\n" + textOfNWords(10)),
		proseResult("still-short\n\n" + textOfNWords(10)),
		proseResult(textOfNWords(20)),
	}}
	engine := &Engine{ollamaGen: gen, log: zap.NewNop()}
	engine.ConfigureSegmentValidation(15, 10, 1)

	_, err := engine.Generate(context.Background(), segmentPlan(20))
	if err == nil || !errors.Is(err, scriptpkg.ErrSegmentValidationFailed) {
		t.Fatalf("expected bounded segment failure, got %v", err)
	}
	if len(gen.prompts) != 2 {
		t.Fatalf("provider calls = %d, want initial + one retry", len(gen.prompts))
	}
}

func TestEngineGenerate_ExplicitSegmentSourceFallbackPreservesCardinality(t *testing.T) {
	gen := &sequentialSegmentGenerator{results: []*scriptports.GenerationResult{
		proseResult("one paragraph"), proseResult("still one paragraph"),
	}}
	engine := &Engine{ollamaGen: gen, log: zap.NewNop()}
	engine.ConfigureSegmentValidation(15, 10, 1)
	plan := &scriptpkg.ResolvedGenerationPlan{
		TargetWords: 20,
		Segments: []scriptpkg.ScriptSegment{
			{ID: "one", SourceText: "caller scene one"},
			{ID: "two", SourceText: "caller scene two"},
		},
	}
	result, err := engine.Generate(context.Background(), plan)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if result.Output.Text != "caller scene one\n\ncaller scene two" {
		t.Fatalf("fallback text = %q", result.Output.Text)
	}
}

func TestAssembleFrozenSegmentsDoesNotChangeOrder(t *testing.T) {
	texts := []string{"first frozen text", "second frozen text", "third frozen text"}
	if got := assembleFrozenSegments(texts); got != strings.Join(texts, "\n\n") {
		t.Fatalf("assembled text changed order: %q", got)
	}
}
