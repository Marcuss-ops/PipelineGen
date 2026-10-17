package scriptgeneration

import (
	"context"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// countingSemanticAnalyzer records every analyze call, so the single-call
// invariant (RUST-02) can be asserted per scene.
type countingSemanticAnalyzer struct {
	calls  int
	bundle *SemanticCandidateBundle
}

func (f *countingSemanticAnalyzer) SemanticCandidates(_ context.Context, _ string, sourceText string, _ int) (*SemanticCandidateBundle, error) {
	f.calls++
	return &SemanticCandidateBundle{
		Bullets: []BulletCandidate{{Text: strings.TrimSpace(sourceText), SentenceIndex: 0, Start: 0, End: len(sourceText)}},
		Charts:  []ChartValueCandidate{{Text: "42%", Value: 42}},
		Temporal: []TemporalFactCandidate{{
			Text: "15 marzo 2008", NormalizedDate: "2008-03-15",
			Precision: "day", Start: 0, End: 13,
		}},
	}, nil
}

// countingLegacyAnalyzer records calls made through the old ports.
type countingLegacyAnalyzer struct {
	bulletCalls int
	chartCalls  int
}

func (f *countingLegacyAnalyzer) BulletCandidates(context.Context, string, string, int) ([]BulletCandidate, error) {
	f.bulletCalls++
	return nil, nil
}

func (f *countingLegacyAnalyzer) ChartCandidates(context.Context, string, string, int) ([]ChartValueCandidate, error) {
	f.chartCalls++
	return nil, nil
}

func semanticInputs() []scriptpkg.SceneAnalysisInput {
	return []scriptpkg.SceneAnalysisInput{{SceneID: "scene_01", Text: "Il giro del 42% cambiò tutto il 15 marzo 2008."}}
}

func semanticHighlights() []scriptpkg.SceneHighlight {
	return []scriptpkg.SceneHighlight{{SceneID: "scene_01"}}
}

func testRunner(analyzer SceneSemanticAnalyzer) *Runner {
	r := &Runner{log: zap.NewNop()}
	r.SetSceneSemanticAnalyzer(analyzer)
	return r
}

func TestAttachVisualSemanticsPerformsOneCallPerScene(t *testing.T) {
	analyzer := &countingSemanticAnalyzer{}
	r := testRunner(analyzer)
	out := r.attachVisualSemantics(context.Background(), "it", semanticInputs(), semanticHighlights(), true)
	if analyzer.calls != 1 {
		t.Fatalf("analyze calls=%d, want exactly 1 per scene", analyzer.calls)
	}
	if len(out) != 1 || len(out[0].Bullets) == 0 {
		t.Fatalf("bullets not applied: %+v", out)
	}
	if len(out[0].ChartValues) == 0 {
		t.Fatal("chart values not applied when the gate is open")
	}
}

func TestAttachVisualSemanticsRespectsClosedChartGate(t *testing.T) {
	analyzer := &countingSemanticAnalyzer{}
	r := testRunner(analyzer)
	out := r.attachVisualSemantics(context.Background(), "it", semanticInputs(), semanticHighlights(), false)
	if analyzer.calls != 1 {
		t.Fatalf("analyze calls=%d, want exactly 1", analyzer.calls)
	}
	if len(out[0].ChartValues) != 0 {
		t.Fatal("chart values must not be merged when the gate is closed")
	}
}

func TestAttachVisualSemanticsEmptyTextSkipsWorker(t *testing.T) {
	analyzer := &countingSemanticAnalyzer{}
	r := testRunner(analyzer)
	inputs := []scriptpkg.SceneAnalysisInput{{SceneID: "scene_01", Text: "   "}}
	r.attachVisualSemantics(context.Background(), "it", inputs, semanticHighlights(), true)
	if analyzer.calls != 0 {
		t.Fatalf("analyze calls=%d, want 0 for an empty scene text", analyzer.calls)
	}
}

func TestAttachVisualSemanticsFailureKeepsExistingValues(t *testing.T) {
	r := testRunner(failSemanticAnalyzer{})
	inputs := semanticInputs()
	highlights := []scriptpkg.SceneHighlight{{SceneID: "scene_01", Bullets: []scriptpkg.SceneBulletSpan{{Text: "keep"}}}}
	out := r.attachVisualSemantics(context.Background(), "it", inputs, highlights, true)
	if len(out[0].Bullets) != 1 || out[0].Bullets[0].Text != "keep" {
		t.Fatalf("existing bullets must survive an analysis failure: %+v", out[0].Bullets)
	}
}

type failSemanticAnalyzer struct{}

func (failSemanticAnalyzer) SemanticCandidates(context.Context, string, string, int) (*SemanticCandidateBundle, error) {
	return nil, context.Canceled
}

func TestLegacyFallbackStillWiredWhenSemanticAnalyzerAbsent(t *testing.T) {
	r := &Runner{}
	legacy := &countingLegacyAnalyzer{}
	r.SetSceneBulletAnalyzer(legacy)
	r.SetSceneChartAnalyzer(legacy)
	out := r.attachVisualBullets(context.Background(), "it", semanticInputs(), semanticHighlights())
	if legacy.bulletCalls != 1 || len(out) != 1 {
		t.Fatalf("legacy bullet path changed: calls=%d", legacy.bulletCalls)
	}
	_ = r.attachVisualChartValues(context.Background(), "it", semanticInputs(), semanticHighlights())
	if legacy.chartCalls != 1 {
		t.Fatalf("legacy chart path changed: calls=%d", legacy.chartCalls)
	}
}
