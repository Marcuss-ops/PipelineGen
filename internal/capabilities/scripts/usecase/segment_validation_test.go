package usecase

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
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

func TestNormalizeGeneratedSegment_RemovesPresentationNoise(t *testing.T) {
	raw := "### Segment 1\n\nTesla changed the market.\n\nElectric vehicles became more common."
	got := normalizeGeneratedSegment(raw)
	if got != "Tesla changed the market. Electric vehicles became more common." {
		t.Fatalf("normalized segment = %q", got)
	}
	if len(splitGeneratedSegmentParagraphs(raw)) != 1 {
		t.Fatal("presentation line breaks must not create multiple segment paragraphs")
	}
}

func TestEngineGenerate_UsesGlobalSourceTextFallback(t *testing.T) {
	gen := &sequentialSegmentGenerator{results: []*scriptports.GenerationResult{
		proseResult(""), proseResult(""),
	}}
	engine := &Engine{ollamaGen: gen, log: zap.NewNop()}
	engine.ConfigureSegmentValidation(15, 10, 1)
	plan := &scriptpkg.ResolvedGenerationPlan{
		Title: "fallback", Topic: "fallback", Language: "en", Mode: "text", TargetWords: 10,
		SourceText: "authoritative source text for this segment",
		Segments:   []scriptpkg.ScriptSegment{{ID: "one", Topic: "one", TargetWords: 10}},
	}
	result, err := engine.Generate(context.Background(), plan)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if result.Output.Text == "" {
		t.Fatal("expected deterministic source fallback")
	}
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
		proseResult(textOfNWords(10)),
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
	if !strings.Contains(gen.prompts[0], "Topic: one") || !strings.Contains(gen.prompts[1], "Topic: two") {
		t.Fatalf("segment prompts lost canonical topic ownership: %q", gen.prompts)
	}
}

func TestEngineGenerate_PerSegmentRequestsCarryOnlyOwnedEditorialEvidence(t *testing.T) {
	gen := &sequentialSegmentGenerator{results: []*scriptports.GenerationResult{
		proseResult("paul one two three"),
		proseResult("andrew one two three"),
	}}
	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:       "evidence routing",
		Language:    "en",
		Mode:        "clip_to_script",
		TargetWords: 8,
		Segments: []scriptpkg.ScriptSegment{
			{ID: "paul", Topic: "Paul Giamatti", SourceText: "PAUL_EDITORIAL_82931", ClipIDs: []string{"clipA"}, TargetWords: 4},
			{ID: "andrew", Topic: "Andrew Garfield", SourceText: "ANDREW_EDITORIAL_18372", ClipIDs: []string{"clipB"}, TargetWords: 4},
		},
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clipA", "clipB"},
			SegmentEvidence: []scriptpkg.SegmentClipEvidence{
				{SegmentID: "paul", Topic: "Paul Giamatti", SourceText: "PAUL_EDITORIAL_82931", ClipIDs: []string{"clipA"}, Clips: map[string]scriptpkg.ClipDetail{
					"clipA": {Description: "PAUL_CLIP_A_FACT_74211", Transcript: "PAUL_CLIP_A_TRANSCRIPT_74211"},
				}},
				{SegmentID: "andrew", Topic: "Andrew Garfield", SourceText: "ANDREW_EDITORIAL_18372", ClipIDs: []string{"clipB"}, Clips: map[string]scriptpkg.ClipDetail{
					"clipB": {Description: "ANDREW_CLIP_B_FACT_18372", Transcript: "ANDREW_CLIP_B_TRANSCRIPT_18372"},
				}},
			},
		},
	}

	engine := &Engine{ollamaGen: gen, log: zap.NewNop()}
	engine.ConfigureSegmentValidation(15, 10, 0)
	if _, err := engine.Generate(context.Background(), plan); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if len(gen.prompts) != 2 {
		t.Fatalf("provider calls = %d, want one per segment", len(gen.prompts))
	}
	for i, prompt := range gen.prompts {
		var own, other, ownClip, otherClip string
		switch {
		case strings.Contains(prompt, "Topic: Paul Giamatti"):
			own, other, ownClip, otherClip = "PAUL_EDITORIAL_82931", "ANDREW_EDITORIAL_18372", "PAUL_CLIP_A_FACT_74211", "ANDREW_CLIP_B_FACT_18372"
		case strings.Contains(prompt, "Topic: Andrew Garfield"):
			own, other, ownClip, otherClip = "ANDREW_EDITORIAL_18372", "PAUL_EDITORIAL_82931", "ANDREW_CLIP_B_FACT_18372", "PAUL_CLIP_A_FACT_74211"
		default:
			t.Errorf("request[%d] has no recognized segment topic: %s", i, prompt)
			continue
		}
		if !strings.Contains(prompt, own) || !strings.Contains(prompt, ownClip) {
			t.Errorf("request[%d] lost owned evidence: %s", i, prompt)
		}
		if strings.Contains(prompt, other) || strings.Contains(prompt, otherClip) {
			t.Errorf("request[%d] contains evidence owned by another segment: %s", i, prompt)
		}
	}
}

func TestEngineGenerate_SegmentRegenerationStopsAtRetryLimit(t *testing.T) {
	gen := &sequentialSegmentGenerator{results: []*scriptports.GenerationResult{
		proseResult("short\n\nshort"),
		proseResult("still-short\n\nstill-short"),
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

func TestEngineGenerate_DoesNotUseSegmentSourceAsFinalNarration(t *testing.T) {
	gen := &sequentialSegmentGenerator{results: []*scriptports.GenerationResult{
		proseResult("one paragraph"), proseResult("still one paragraph"),
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
	_, err := engine.Generate(context.Background(), plan)
	if err == nil || !errors.Is(err, scriptpkg.ErrSegmentValidationFailed) {
		t.Fatalf("expected segment validation failure instead of source_text fallback, got %v", err)
	}
}

func TestAssembleFrozenSegmentsDoesNotChangeOrder(t *testing.T) {
	texts := []string{"first frozen text", "second frozen text", "third frozen text"}
	if got := assembleFrozenSegments(texts); got != strings.Join(texts, "\n\n") {
		t.Fatalf("assembled text changed order: %q", got)
	}
}

func TestSourceTextFallbackParagraphHonorsBounds(t *testing.T) {
	got := sourceTextFallbackParagraph("Elon Musk spoke in Texas about electric vehicles.", segmentBudget{Target: 10, Min: 8, Max: 12})
	words := textutil.CountWords(got)
	if words < 8 || words > 12 {
		t.Fatalf("fallback words=%d, want 8-12: %q", words, got)
	}
	if !strings.Contains(got, "Elon Musk") || !strings.Contains(got, "Texas") {
		t.Fatalf("fallback lost authored entities: %q", got)
	}
}
