package rustexec

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	coreasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// phraseImpactFakeRunner records every request it serves and answers from a
// scripted queue, so the adapter's request/response contract is asserted
// without a real Rust binary.
type phraseImpactFakeRunner struct {
	requests []byte
	replies  []string
	runErr   error
}

func (f *phraseImpactFakeRunner) Run(_ context.Context, _ string, input []byte, _ int64) ([]byte, []byte, error) {
	f.requests = append(f.requests, input...)
	if f.runErr != nil {
		return nil, []byte("boom"), f.runErr
	}
	if len(f.replies) == 0 {
		return nil, nil, errors.New("fake runner: no scripted reply")
	}
	reply := f.replies[0]
	f.replies = f.replies[1:]
	return []byte(reply), nil, nil
}

func (f *phraseImpactFakeRunner) requestCount() int {
	return strings.Count(string(f.requests), "\n")
}

type phraseImpactFakeEmbedder struct {
	calls   [][]string
	vectors [][]float32
	err     error
}

func (e *phraseImpactFakeEmbedder) EmbedPassagesBatch(_ context.Context, texts []string) ([]coreasset.EmbeddingResult, error) {
	e.calls = append(e.calls, texts)
	if e.err != nil {
		return nil, e.err
	}
	out := make([]coreasset.EmbeddingResult, len(e.vectors))
	for i := range e.vectors {
		out[i] = coreasset.EmbeddingResult{Vector: e.vectors[i]}
	}
	return out, nil
}

func TestPhraseImpactAnalyzerFallsBackToLexicalModeWithoutEmbedder(t *testing.T) {
	runner := &phraseImpactFakeRunner{replies: []string{
		`{"ok":true,"result":{"summary":"La squadra vinse.","bullet_points":[{"text":"Campionato vinto"}],"heavy_sentences":[{"index":0,"text":"La squadra vinse il campionato.","importance":0.9}]}}`,
	}}
	analyzer := NewPhraseImpactAnalyzer("bin/phrase_impact", runner, nil)

	got, err := analyzer.Analyze(context.Background(), "La squadra vinse il campionato.", "it")
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if got.Summary != "La squadra vinse." {
		t.Fatalf("summary = %q", got.Summary)
	}
	if len(got.BulletPoints) != 1 || got.BulletPoints[0] != "Campionato vinto" {
		t.Fatalf("bullet points = %#v", got.BulletPoints)
	}
	if len(got.HeavySentences) != 1 || got.HeavySentences[0].Index != 0 {
		t.Fatalf("heavy sentences = %#v", got.HeavySentences)
	}

	var request struct {
		LexicalOnly bool   `json:"lexical_only"`
		Language    string `json:"language"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(runner.requests))), &request); err != nil {
		t.Fatalf("decode request: %v (%s)", err, runner.requests)
	}
	if !request.LexicalOnly {
		t.Fatal("without an embedder the adapter must explicitly request lexical_only scoring")
	}
	if request.Language != "it" {
		t.Fatalf("language = %q", request.Language)
	}
}

func TestPhraseImpactAnalyzerUsesCanonicalSentenceSplitAndPassageVectors(t *testing.T) {
	runner := &phraseImpactFakeRunner{replies: []string{
		`{"ok":true,"sentences":[{"text":"Il Milan vinse.","start_byte":0,"end_byte":15},{"text":"Poi celebrò!","start_byte":16,"end_byte":29}]}`,
		`{"ok":true,"result":{"summary":"Il Milan vinse.","bullet_points":[],"heavy_sentences":[]}}`,
	}}
	embedder := &phraseImpactFakeEmbedder{vectors: [][]float32{{1, 0}, {0, 1}}}
	analyzer := NewPhraseImpactAnalyzer("bin/phrase_impact", runner, embedder)

	if _, err := analyzer.Analyze(context.Background(), "Il Milan vinse. Poi celebrò!", "it"); err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if runner.requestCount() != 2 {
		t.Fatalf("expected a split request followed by the analysis request, got %d requests", runner.requestCount())
	}
	if len(embedder.calls) != 1 || len(embedder.calls[0]) != 2 {
		t.Fatalf("embedder calls = %#v, want one batch of the split sentences", embedder.calls)
	}
	var analysis struct {
		Embeddings  [][]float32 `json:"embeddings"`
		LexicalOnly bool        `json:"lexical_only"`
		EmbeddingMS float64     `json:"embedding_ms"`
	}
	lines := strings.Split(strings.TrimSpace(string(runner.requests)), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &analysis); err != nil {
		t.Fatalf("decode analysis request: %v", err)
	}
	if analysis.LexicalOnly {
		t.Fatal("with passage vectors the adapter must not request lexical-only scoring")
	}
	if len(analysis.Embeddings) != 2 {
		t.Fatalf("embeddings = %#v, want one vector per sentence", analysis.Embeddings)
	}
	if analysis.EmbeddingMS <= 0 {
		t.Fatal("embedding_ms must record the E5 wall time so the Rust stage timings stay honest")
	}
}

func TestPhraseImpactAnalyzerRejectsEmbeddingCountMismatch(t *testing.T) {
	runner := &phraseImpactFakeRunner{replies: []string{
		`{"ok":true,"sentences":[{"text":"One.","start_byte":0,"end_byte":4},{"text":"Two.","start_byte":5,"end_byte":9}]}`,
	}}
	embedder := &phraseImpactFakeEmbedder{vectors: [][]float32{{1, 0}}}
	analyzer := NewPhraseImpactAnalyzer("bin/phrase_impact", runner, embedder)

	if _, err := analyzer.Analyze(context.Background(), "One. Two.", "en"); err == nil {
		t.Fatal("a vector/sentence count mismatch must fail closed rather than misalign scores")
	}
}

func TestPhraseImpactAnalyzerRejectsInvalidSentenceByteOffsets(t *testing.T) {
	runner := &phraseImpactFakeRunner{replies: []string{
		`{"ok":true,"sentences":[{"text":"Milan." ,"start_byte":0,"end_byte":5}]}`,
	}}
	embedder := &phraseImpactFakeEmbedder{vectors: [][]float32{{1, 0}}}
	analyzer := NewPhraseImpactAnalyzer("bin/phrase_impact", runner, embedder)
	if _, err := analyzer.Analyze(context.Background(), "Il Milan vinse.", "it"); err == nil || !strings.Contains(err.Error(), "invalid UTF-8 byte offsets") {
		t.Fatalf("Analyze error = %v, want invalid source offsets rejected before embedding", err)
	}
	if len(embedder.calls) != 0 {
		t.Fatalf("embedder was called with invalid source offsets: %#v", embedder.calls)
	}
}

func TestPhraseImpactAnalyzerEmptyTranscriptSkipsTheWorker(t *testing.T) {
	runner := &phraseImpactFakeRunner{}
	analyzer := NewPhraseImpactAnalyzer("bin/phrase_impact", runner, nil)

	got, err := analyzer.Analyze(context.Background(), "   ", "it")
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if got.Summary != "" || len(got.BulletPoints) != 0 || len(got.HeavySentences) != 0 {
		t.Fatalf("empty transcript must produce an empty result, got %#v", got)
	}
	if runner.requestCount() != 0 {
		t.Fatal("empty transcript must not invoke the Rust worker")
	}
}

func TestPhraseImpactAnalyzerUnconfiguredBinaryFailsClosed(t *testing.T) {
	analyzer := NewPhraseImpactAnalyzer("", &phraseImpactFakeRunner{}, nil)
	if _, err := analyzer.Analyze(context.Background(), "Some narration.", "en"); err == nil {
		t.Fatal("an unconfigured Rust binary must fail closed, not silently return an empty summary")
	}
}

// TestPhraseImpactAnalyzerPropagatesWorkerError pins the failure surface: the
// runner's stderr is preserved so an operator can diagnose a crashed worker.
func TestPhraseImpactAnalyzerPropagatesWorkerError(t *testing.T) {
	runner := &phraseImpactFakeRunner{runErr: errors.New("exit status 1")}
	analyzer := NewPhraseImpactAnalyzer("bin/phrase_impact", runner, nil)

	_, err := analyzer.Analyze(context.Background(), "Some narration.", "en")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %v, want the worker stderr included", err)
	}
}

// TestPhraseImpactResultIsTheKernelContract keeps the platform adapter from
// re-declaring its own result type: the assertion only compiles while the
// adapter returns the kernel-owned shape.
func TestPhraseImpactResultIsTheKernelContract(t *testing.T) {
	var result scriptpkg.PhraseImpactResult
	runner := &phraseImpactFakeRunner{replies: []string{
		`{"ok":true,"result":{"summary":"ok","bullet_points":[],"heavy_sentences":[]}}`,
	}}
	analyzer := NewPhraseImpactAnalyzer("bin/phrase_impact", runner, nil)
	got, err := analyzer.Analyze(context.Background(), "Narration.", "en")
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	result = got
	if result.Summary != "ok" {
		t.Fatalf("summary = %q", result.Summary)
	}
}

// TestPhraseImpactAnalyzerDecodesWorkerStageTimings keeps the worker's own
// stage breakdown on the result. It is the only place the production embedding
// cost becomes visible, so a dropped field would be a silent measurement hole.
func TestPhraseImpactAnalyzerDecodesWorkerStageTimings(t *testing.T) {
	runner := &phraseImpactFakeRunner{replies: []string{
		`{"ok":true,"result":{"summary":"ok","bullet_points":[],"heavy_sentences":[],"timings":{"split_ms":0.4,"embedding_ms":12.5,"similarity_ms":1.1,"ranking_ms":0.9,"summary_ms":2.2,"bullet_ms":0.1,"total_ms":17.2}}}`,
	}}
	analyzer := NewPhraseImpactAnalyzer("bin/phrase_impact", runner, nil)

	got, err := analyzer.Analyze(context.Background(), "Some narration.", "en")
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if got.Timings.EmbeddingMS != 12.5 || got.Timings.TotalMS != 17.2 || got.Timings.SummaryMS != 2.2 {
		t.Fatalf("timings = %+v, want the worker-reported stage breakdown", got.Timings)
	}
}

// TestPhraseImpactAnalyzerRunsTheRealRustWorker is the end-to-end check of the
// delivered artifact: the Go adapter drives the actual pipelinegen-muscles
// binary in its phrase-impact mode over the real NDJSON contract. It skips
// when no binary is available so the suite stays hermetic.
func TestPhraseImpactAnalyzerRunsTheRealRustWorker(t *testing.T) {
	binary := os.Getenv("PHRASE_IMPACT_BIN")
	if binary == "" {
		if _, err := os.Stat("bin/phrase_impact"); err == nil {
			binary = "bin/phrase_impact"
		}
	}
	if binary == "" {
		t.Skip("phrase-impact Rust binary not available; set PHRASE_IMPACT_BIN to run this check")
	}

	analyzer := NewPhraseImpactAnalyzer(binary, nil, nil)
	transcript := "La squadra vinse il campionato nazionale. Il risultato cambiò la storia del club per sempre. I tifosi celebrarono tutta la notte in piazza."
	got, err := analyzer.Analyze(context.Background(), transcript, "it")
	if err != nil {
		t.Fatalf("Analyze against the real Rust worker: %v", err)
	}
	if strings.TrimSpace(got.Summary) == "" {
		t.Fatal("the real Rust worker must return an extractive summary")
	}
	if len(got.BulletPoints) == 0 {
		t.Fatal("the real Rust worker must return bullet points")
	}
	if len(got.HeavySentences) == 0 {
		t.Fatal("the real Rust worker must return the ranked heavy sentences")
	}
	for _, sentence := range got.HeavySentences {
		if strings.TrimSpace(sentence.Text) == "" {
			t.Fatalf("heavy sentence must keep its source text: %#v", sentence)
		}
		if !strings.Contains(transcript, strings.TrimSpace(sentence.Text)) {
			t.Fatalf("heavy sentence must be extracted from the narration, got %q", sentence.Text)
		}
	}
}
