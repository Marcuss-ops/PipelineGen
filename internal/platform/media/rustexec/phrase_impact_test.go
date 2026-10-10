package rustexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	coreasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/embeddings"
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
	calls         [][]string
	vectors       [][]float32
	err           error
	batchResponse func([]string) [][]float32
}

func (e *phraseImpactFakeEmbedder) EmbedPassagesBatch(_ context.Context, texts []string) ([]coreasset.EmbeddingResult, error) {
	batchTexts := append([]string(nil), texts...)
	e.calls = append(e.calls, batchTexts)
	if e.err != nil {
		return nil, e.err
	}
	vectors := e.vectors
	if e.batchResponse != nil {
		vectors = e.batchResponse(batchTexts)
	}
	out := make([]coreasset.EmbeddingResult, len(vectors))
	for i := range vectors {
		out[i] = coreasset.EmbeddingResult{Vector: vectors[i]}
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
		LexicalOnly    bool           `json:"lexical_only"`
		Language       string         `json:"language"`
		ChapterOptions map[string]any `json:"chapter_options"`
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
	if request.ChapterOptions["profile_version"] != "segmentation.v1" ||
		request.ChapterOptions["min_sentences"] != float64(5) ||
		request.ChapterOptions["max_sentences"] != float64(36) ||
		request.ChapterOptions["complexity_penalty"] != float64(1) {
		t.Fatalf("chapter options = %#v, want the versioned production profile", request.ChapterOptions)
	}
}

// TestPhraseImpactAnalyzerInjectsTheLexiconPhraseStopWords pins the seam that
// makes keyphrase extraction language-general: the Rust worker owns no word list
// of its own, so the adapter must ship the request language's phrase stop words
// on every call. The expectations are read from the repository's own lexicon
// data (installed by TestMain), which is also what production injects.
func TestPhraseImpactAnalyzerInjectsTheLexiconPhraseStopWords(t *testing.T) {
	const reply = `{"ok":true,"result":{"summary":"s","bullet_points":[],"heavy_sentences":[]}}`
	cases := []struct {
		language string
		must     []string
		mustNot  []string
	}{
		// A fully enumerated language ships its own profile.
		{language: "en", must: []string{"the", "than", "by", "did"}, mustNot: []string{"della"}},
		{language: "it", must: []string{"il", "della", "che"}, mustNot: []string{"the", "than"}},
		// A language the repository does not enumerate degrades to the
		// cross-linguistic fallback profile, never to an unrelated one.
		{language: "xx", must: []string{"the", "della"}, mustNot: []string{"than", "did"}},
		// Region tags resolve to their base language profile.
		{language: "it-IT", must: []string{"della", "il"}, mustNot: []string{"than", "did"}},
	}
	for _, tc := range cases {
		runner := &phraseImpactFakeRunner{replies: []string{reply}}
		analyzer := NewPhraseImpactAnalyzer("bin/phrase_impact", runner, nil)
		if _, err := analyzer.Analyze(context.Background(), "Some narration.", tc.language); err != nil {
			t.Fatalf("Analyze(%q) returned error: %v", tc.language, err)
		}
		var payload struct {
			StopWords []string `json:"stopwords"`
		}
		if err := json.Unmarshal(bytes.TrimSpace(runner.requests), &payload); err != nil {
			t.Fatalf("decode request for %q: %v", tc.language, err)
		}
		set := make(map[string]bool, len(payload.StopWords))
		for _, word := range payload.StopWords {
			set[word] = true
		}
		for _, want := range tc.must {
			if !set[want] {
				t.Errorf("%s: injected stop words must contain %q, got %d words", tc.language, want, len(set))
			}
		}
		for _, unwanted := range tc.mustNot {
			if set[unwanted] {
				t.Errorf("%s: injected stop words must not contain another language's %q", tc.language, unwanted)
			}
		}
	}
}

func TestPhraseImpactAnalyzerPassesScenesAndTopicsAsOptionalContext(t *testing.T) {
	runner := &phraseImpactFakeRunner{replies: []string{
		`{"ok":true,"result":{"summary":"s","bullet_points":[],"heavy_sentences":[],"chapter_manifest":{"schema_version":"chapter_manifest.v1","chapters":[{"title":"Rendering workflow","title_source":"scene_topic","start_sentence":0,"end_sentence":1,"bullets":[{"start_sentence":0,"end_sentence":1,"text":"Rendering workflow is available."}] }]}}}`,
	}}
	analyzer := NewPhraseImpactAnalyzer("bin/phrase_impact", runner, nil)
	got, err := analyzer.AnalyzeWithContext(context.Background(), "Rendering workflow is available.", "en", []string{"Rendering workflow is available."}, []string{"Rendering workflow"})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if got.ChapterManifest.SchemaVersion != "chapter_manifest.v1" || len(got.ChapterManifest.Chapters) != 1 {
		t.Fatalf("chapter manifest = %#v", got.ChapterManifest)
	}
	var payload struct {
		Scenes []string `json:"scenes"`
		Topics []string `json:"scene_topics"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(runner.requests), &payload); err != nil {
		t.Fatalf("decode worker request: %v", err)
	}
	if !reflect.DeepEqual(payload.Scenes, []string{"Rendering workflow is available."}) || !reflect.DeepEqual(payload.Topics, []string{"Rendering workflow"}) {
		t.Fatalf("scene context = %#v / %#v", payload.Scenes, payload.Topics)
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

func TestPhraseImpactAnalyzerChunksPassagesAtSidecarBatchLimitAndPreservesOrder(t *testing.T) {
	const sentenceCount = phraseImpactEmbeddingBatchSize*2 + 5
	segments := make([]string, sentenceCount)
	segmentResponses := make([]string, sentenceCount)
	for i := range segments {
		segments[i] = fmt.Sprintf("S%03d.", i)
		start := i * 6
		segmentResponses[i] = fmt.Sprintf(`{"text":%q,"start_byte":%d,"end_byte":%d}`, segments[i], start, start+len(segments[i]))
	}
	transcript := strings.Join(segments, " ")
	runner := &phraseImpactFakeRunner{replies: []string{
		`{"ok":true,"sentences":[` + strings.Join(segmentResponses, ",") + `]}`,
		`{"ok":true,"result":{"summary":"S000.","bullet_points":[],"heavy_sentences":[]}}`,
	}}
	responseBatch := 0
	embedder := &phraseImpactFakeEmbedder{batchResponse: func(batch []string) [][]float32 {
		responseBatch++
		vectors := make([][]float32, len(batch))
		for i := range batch {
			vectors[i] = []float32{float32(responseBatch), float32(i + 1)}
		}
		return vectors
	}}
	analyzer := NewPhraseImpactAnalyzer("bin/phrase_impact", runner, embedder)
	if _, err := analyzer.Analyze(context.Background(), transcript, "en"); err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if got := batchLengths(embedder.calls); !reflect.DeepEqual(got, []int{32, 32, 5}) {
		t.Fatalf("embedder batch sizes = %v, want [32 32 5]", got)
	}
	for i, batch := range embedder.calls {
		if !reflect.DeepEqual(batch, segments[i*phraseImpactEmbeddingBatchSize:min((i+1)*phraseImpactEmbeddingBatchSize, sentenceCount)]) {
			t.Fatalf("batch %d order/content mismatch: %v", i, batch)
		}
	}
	lines := strings.Split(strings.TrimSpace(string(runner.requests)), "\n")
	var analysis struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &analysis); err != nil {
		t.Fatalf("decode analysis request: %v", err)
	}
	if len(analysis.Embeddings) != sentenceCount {
		t.Fatalf("analysis contains %d embeddings, want %d", len(analysis.Embeddings), sentenceCount)
	}
	for i, vector := range analysis.Embeddings {
		batchIndex := i / phraseImpactEmbeddingBatchSize
		batchOffset := i % phraseImpactEmbeddingBatchSize
		if vector[0] != float32(batchIndex+1) || vector[1] != float32(batchOffset+1) {
			t.Fatalf("vector %d moved during batching: %v", i, vector)
		}
	}
}

func TestPhraseImpactAnalyzerRejectsShortEmbeddingBatch(t *testing.T) {
	segments := make([]string, phraseImpactEmbeddingBatchSize+1)
	responses := make([]string, len(segments))
	for i := range segments {
		segments[i] = fmt.Sprintf("S%02d.", i)
		start := i * 5
		responses[i] = fmt.Sprintf(`{"text":%q,"start_byte":%d,"end_byte":%d}`, segments[i], start, start+len(segments[i]))
	}
	runner := &phraseImpactFakeRunner{replies: []string{
		`{"ok":true,"sentences":[` + strings.Join(responses, ",") + `]}`,
	}}
	embedder := &phraseImpactFakeEmbedder{vectors: make([][]float32, phraseImpactEmbeddingBatchSize-1)}
	analyzer := NewPhraseImpactAnalyzer("bin/phrase_impact", runner, embedder)
	_, err := analyzer.Analyze(context.Background(), strings.Join(segments, " "), "en")
	if err == nil || !strings.Contains(err.Error(), "sentence batch 0..32") {
		t.Fatalf("Analyze error = %v, want truncated first vector batch rejected", err)
	}
	if len(embedder.calls) != 1 {
		t.Fatalf("embedder calls = %v, want fail closed after short first response", batchLengths(embedder.calls))
	}
}

func batchLengths(batches [][]string) []int {
	lengths := make([]int, len(batches))
	for i := range batches {
		lengths[i] = len(batches[i])
	}
	return lengths
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

// TestPhraseImpactAnalyzerUsesCanonicalMultilingualE5ForSyntheticFixture
// exercises the real adapter, local multilingual E5 sidecar, and Rust worker
// against the labeled Italian diagnostic corpus. It is opt-in because it
// requires both local services and several CPU inference batches.
func TestPhraseImpactAnalyzerUsesCanonicalMultilingualE5ForSyntheticFixture(t *testing.T) {
	if os.Getenv("PHRASE_IMPACT_E5_E2E") != "1" {
		t.Skip("set PHRASE_IMPACT_E5_E2E=1 to exercise the local multilingual E5 sidecar")
	}
	binary := os.Getenv("PHRASE_IMPACT_BIN")
	if binary == "" {
		t.Skip("set PHRASE_IMPACT_BIN to the Rust phrase-impact executable")
	}
	serverURL := os.Getenv("EMBEDDING_SERVER_URL")
	if serverURL == "" {
		serverURL = "http://127.0.0.1:8001"
	}
	fixtureDir := filepath.Join("..", "..", "..", "..", "rust", "pipelinegen-muscles", "fixtures")
	transcriptBytes, err := os.ReadFile(filepath.Join(fixtureDir, "elon_musk_synthetic_transcript_it.txt"))
	if err != nil {
		t.Fatalf("read synthetic transcript fixture: %v", err)
	}
	var groundTruth struct {
		HeavySentenceIDs []string `json:"heavy_sentence_ids"`
		Sentences        []struct {
			ID    string `json:"id"`
			Text  string `json:"text"`
			Label string `json:"label"`
		} `json:"sentences"`
	}
	goldBytes, err := os.ReadFile(filepath.Join(fixtureDir, "elon_musk_synthetic_ground_truth.json"))
	if err != nil {
		t.Fatalf("read synthetic ground-truth fixture: %v", err)
	}
	if err := json.Unmarshal(goldBytes, &groundTruth); err != nil {
		t.Fatalf("decode synthetic ground truth: %v", err)
	}
	analyzer := NewPhraseImpactAnalyzer(binary, nil, embeddings.NewHTTPTextEmbedderWithTimeout(serverURL, 2*time.Minute).(*embeddings.HTTPTextEmbedder))
	transcript := string(transcriptBytes)
	got, err := analyzer.Analyze(context.Background(), transcript, "it")
	if err != nil {
		t.Fatalf("analyze fixture with multilingual E5 sidecar: %v", err)
	}
	splitPayload, err := json.Marshal(map[string]string{"operation": "split_sentences", "transcript": transcript, "language": "it"})
	if err != nil {
		t.Fatalf("marshal source split request: %v", err)
	}
	splitOutput, splitStderr, err := analyzer.runner.Run(context.Background(), binary, append(splitPayload, '\n'), phraseImpactOutputLimit)
	if err != nil {
		t.Fatalf("split source for extractiveness check: %v: %s", err, splitStderr)
	}
	var split phraseImpactResponse
	if err := json.Unmarshal(splitOutput, &split); err != nil || !split.OK {
		t.Fatalf("decode source sentence split: err=%v response=%s", err, splitOutput)
	}
	sourceSentences := make([]string, len(split.Sentences))
	for i, sentence := range split.Sentences {
		sourceSentences[i] = sentence.Text
	}
	if !phraseImpactSummaryIsExtractive(got.Summary, sourceSentences) {
		t.Fatalf("E5 summary must contain only complete source sentences: %q", got.Summary)
	}
	for _, bullet := range got.BulletPoints {
		if !containsExactSentence(sourceSentences, bullet) {
			t.Fatalf("E5 bullet must be an exact source sentence: %q", bullet)
		}
	}
	if strings.Contains(strings.ToLower(got.Summary), "licenziamenti") && !strings.Contains(strings.ToLower(got.Summary), "non avrebbe annunciato licenziamenti") {
		t.Fatalf("summary mentioned layoffs but dropped source negation: %q", got.Summary)
	}
	heavy := make(map[string]bool, len(groundTruth.HeavySentenceIDs))
	for _, id := range groundTruth.HeavySentenceIDs {
		heavy[id] = true
	}
	idByText := make(map[string]string, len(groundTruth.Sentences))
	for _, sentence := range groundTruth.Sentences {
		idByText[sentence.Text] = sentence.ID
	}
	metrics := make(map[string]float64)
	for _, cutoff := range []int{5, 10} {
		limit := min(cutoff, len(got.HeavySentences))
		hits := 0
		for _, sentence := range got.HeavySentences[:limit] {
			if heavy[idByText[sentence.Text]] {
				hits++
			}
		}
		if limit > 0 {
			metrics[fmt.Sprintf("precision_at_%d", cutoff)] = float64(hits) / float64(limit)
		}
		if len(heavy) > 0 {
			metrics[fmt.Sprintf("recall_heavy_at_%d", cutoff)] = float64(hits) / float64(len(heavy))
		}
	}
	t.Logf("developer-authored synthetic E5 diagnostic (not human certification): %v", metrics)
}

func phraseImpactSummaryIsExtractive(summary string, sourceSentences []string) bool {
	remaining := strings.TrimSpace(summary)
	if remaining == "" {
		return false
	}
	ordered := append([]string(nil), sourceSentences...)
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for remaining != "" {
		matched := false
		for _, sentence := range ordered {
			if strings.HasPrefix(remaining, sentence) {
				remaining = strings.TrimSpace(strings.TrimPrefix(remaining, sentence))
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func containsExactSentence(sentences []string, candidate string) bool {
	for _, sentence := range sentences {
		if candidate == sentence {
			return true
		}
	}
	return false
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
