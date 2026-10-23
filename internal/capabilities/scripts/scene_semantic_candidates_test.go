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
	calls int
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

// countingBatchAnalyzer records per-scene and batch calls so the runner's
// preference for ONE batch call can be asserted.
type countingBatchAnalyzer struct {
	sceneCalls int
	batchCalls int
	scenes     int
}

func (f *countingBatchAnalyzer) SemanticCandidates(context.Context, string, string, int) (*SemanticCandidateBundle, error) {
	f.sceneCalls++
	return &SemanticCandidateBundle{}, nil
}

func (f *countingBatchAnalyzer) SemanticBundles(_ context.Context, _ string, scenes []SemanticSceneInput) (map[string]*SemanticCandidateBundle, error) {
	f.batchCalls++
	f.scenes = len(scenes)
	out := make(map[string]*SemanticCandidateBundle, len(scenes))
	for _, scene := range scenes {
		out[scene.SceneID] = &SemanticCandidateBundle{
			Bullets: []BulletCandidate{{Text: strings.TrimSpace(scene.Text), Start: 0, End: len(scene.Text)}},
		}
	}
	return out, nil
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
	if len(out) != 1 || len(out[0].Bullets) != 0 {
		t.Fatalf("semantic candidates must not replace Phrase Impact's empty editorial selection: %+v", out)
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

func TestAttachVisualSemanticsUsesOneBatchCallForTheWholeScript(t *testing.T) {
	analyzer := &countingBatchAnalyzer{}
	r := testRunner(analyzer)
	inputs := []scriptpkg.SceneAnalysisInput{
		{SceneID: "scene_01", Text: "Prima scena con Marco Rossi."},
		{SceneID: "scene_02", Text: "Seconda scena con il 42% dei tecnici."},
		{SceneID: "scene_03", Text: "Terza scena con il 15 marzo 2008."},
	}
	highlights := []scriptpkg.SceneHighlight{{SceneID: "scene_01"}, {SceneID: "scene_02"}, {SceneID: "scene_03"}}
	out := r.attachVisualSemantics(context.Background(), "it", inputs, highlights, true)
	if analyzer.batchCalls != 1 || analyzer.sceneCalls != 0 {
		t.Fatalf("batch calls=%d scene calls=%d, want exactly 1 batch and 0 per-scene", analyzer.batchCalls, analyzer.sceneCalls)
	}
	if analyzer.scenes != 3 {
		t.Fatalf("batch carried %d scenes, want 3", analyzer.scenes)
	}
	for i := range out {
		if len(out[i].Bullets) != 0 {
			t.Fatalf("scene %q gained a bullet without Phrase Impact selection: %+v", out[i].SceneID, out[i])
		}
	}
}

func TestSemanticBulletSpansMapSceneLocalUTF8EvidenceToTranscript(t *testing.T) {
	transcript := "Apertura.\n\nÈ qui il Museo Nazionale che custodisce la collezione storica."
	text := "È qui il Museo Nazionale che custodisce la collezione storica."
	start := strings.Index(transcript, text)
	inputs := []scriptpkg.SceneAnalysisInput{{
		SceneID: "scene_01", Text: text, StartByte: &start,
	}}
	end := start + len(text)
	inputs[0].EndByte = &end
	bundle := &SemanticCandidateBundle{Bullets: []BulletCandidate{{Text: text, Start: 0, End: len(text)}}}
	spans, ok := semanticBulletSpans(transcript, inputs, map[string]*SemanticCandidateBundle{"scene_01": bundle})
	if !ok || len(spans) != 1 {
		t.Fatalf("spans=%+v ok=%v, want one grounded semantic candidate", spans, ok)
	}
	if spans[0].StartByte != start || spans[0].EndByte != end || transcript[spans[0].StartByte:spans[0].EndByte] != text {
		t.Fatalf("candidate lost transcript UTF-8 grounding: %+v", spans[0])
	}
}
