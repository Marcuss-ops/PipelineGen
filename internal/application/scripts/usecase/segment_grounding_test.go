// Package usecase — segment_grounding_test.go certifies the grounding
// contract for per-segment generation: a segment that declares no
// source_text must keep the plan's global source (e.g. the research source
// text), while an explicit per-segment source_text overrides it. This pins
// the fix that previously dropped the global source to an empty string and
// left research-grounded segments unanchored.
package usecase

import (
	"context"
	"sync"
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"go.uber.org/zap"
)

// sourceCapturingGenerator records the SourceText of every per-segment
// generation request so tests can assert what grounding reached the model.
type sourceCapturingGenerator struct {
	mu          sync.Mutex
	sourceTexts []string
	results     []*scriptports.GenerationResult
}

func (g *sourceCapturingGenerator) GenerateScript(_ context.Context, req scriptports.TextGenerationRequest) (*scriptports.GenerationResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sourceTexts = append(g.sourceTexts, req.SourceText)
	if len(g.results) == 0 {
		return proseResult(textOfNWords(4)), nil
	}
	r := g.results[0]
	g.results = g.results[1:]
	return r, nil
}

func (g *sourceCapturingGenerator) sources() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.sourceTexts...)
}

const globalSourceSentinel = "GLOBAL_RESEARCH_SOURCE_SENTINEL"

func groundingPlan(segments []scriptpkg.ScriptSegment) *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{
		Title:       "segment grounding",
		Language:    "en",
		Mode:        "text",
		SourceText:  globalSourceSentinel,
		TargetWords: 8,
		Segments:    segments,
	}
}

func newGroundingEngine(gen scriptports.ScriptGenerator) *Engine {
	engine := &Engine{ollamaGen: gen, log: zap.NewNop()}
	engine.ConfigureSegmentValidation(15, 10, 0)
	return engine
}

func TestEngineGenerate_SegmentsWithoutSourceTextKeepGlobalSource(t *testing.T) {
	gen := &sourceCapturingGenerator{}
	engine := newGroundingEngine(gen)

	plan := groundingPlan([]scriptpkg.ScriptSegment{
		{ID: "one", Topic: "one", TargetWords: 4},
		{ID: "two", Topic: "two", TargetWords: 4},
	})
	if _, err := engine.Generate(context.Background(), plan); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	sources := gen.sources()
	if len(sources) != 2 {
		t.Fatalf("provider calls = %d, want one per segment (2)", len(sources))
	}
	for i, src := range sources {
		if src != globalSourceSentinel {
			t.Errorf("request[%d].SourceText = %q, want the global source %q preserved", i, src, globalSourceSentinel)
		}
	}
}

func TestEngineGenerate_SegmentSourceTextOverridesGlobalSource(t *testing.T) {
	gen := &sourceCapturingGenerator{}
	engine := newGroundingEngine(gen)

	plan := groundingPlan([]scriptpkg.ScriptSegment{
		{ID: "one", Topic: "one", SourceText: "OWN_SEGMENT_SOURCE", TargetWords: 4},
		{ID: "two", Topic: "two", TargetWords: 4},
	})
	if _, err := engine.Generate(context.Background(), plan); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	sources := gen.sources()
	if len(sources) != 2 {
		t.Fatalf("provider calls = %d, want one per segment (2)", len(sources))
	}
	if sources[0] != "OWN_SEGMENT_SOURCE" {
		t.Errorf("request[0].SourceText = %q, want the explicit per-segment source", sources[0])
	}
	if sources[1] != globalSourceSentinel {
		t.Errorf("request[1].SourceText = %q, want the global source for the segment without its own source_text", sources[1])
	}
}
