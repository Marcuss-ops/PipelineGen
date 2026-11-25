package voiceover

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	voiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
)

func TestSplitVoiceoverWordsBoundariesAndReconstruction(t *testing.T) {
	for _, n := range []int{399, 400, 401, 800, 801, 1200, 1201} {
		words := make([]string, n)
		for i := range words {
			words[i] = "parola"
		}
		chunks := splitVoiceoverWords(strings.Join(words, " "), 400)
		want := (n + 399) / 400
		if len(chunks) != want {
			t.Fatalf("words=%d: chunks=%d want=%d", n, len(chunks), want)
		}
		var joined []string
		for _, chunk := range chunks {
			got := len(strings.Fields(chunk))
			if got == 0 || got > 400 {
				t.Fatalf("words=%d: invalid chunk size %d", n, got)
			}
			joined = append(joined, strings.Fields(chunk)...)
		}
		if len(joined) != n {
			t.Fatalf("words=%d: reconstructed=%d", n, len(joined))
		}
	}
}

func TestSplitVoiceoverWordsPrefersSentenceBoundaries(t *testing.T) {
	words := make([]string, 0, 401)
	for i := 0; i < 399; i++ {
		words = append(words, "parola")
	}
	words = append(words, "completa.", "Frase", "successiva.")
	chunks := splitVoiceoverWords(strings.Join(words, " "), 400)
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(chunks))
	}
	if !strings.HasSuffix(chunks[0], "completa.") {
		t.Fatalf("first chunk did not end at sentence boundary: %q", chunks[0][len(chunks[0])-30:])
	}
}

func TestSplitVoiceoverWordsKeepsSentencesWhole(t *testing.T) {
	first := voiceoverTestSentence(100)
	second := voiceoverTestSentence(50)
	chunks := splitVoiceoverWords(first+" "+second, 120)
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(chunks))
	}
	if chunks[0] != first {
		t.Fatalf("first chunk must be the whole first sentence, got %q", chunks[0])
	}
	if chunks[1] != second {
		t.Fatalf("second chunk must be the whole second sentence, got %q", chunks[1])
	}
}

func TestSplitVoiceoverWordsDoesNotTreatColonAsSentenceEnd(t *testing.T) {
	const text = "Ecco: una due tre quattro cinque sei. Altra frase qui."
	chunks := splitVoiceoverWords(text, 8)
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2: %q", len(chunks), chunks)
	}
	if chunks[0] != "Ecco: una due tre quattro cinque sei." {
		t.Fatalf("colon split the sentence: %q", chunks[0])
	}
	if chunks[1] != "Altra frase qui." {
		t.Fatalf("second chunk=%q", chunks[1])
	}
}

func TestSplitVoiceoverWordsBreaksOversizedSentenceOnClauses(t *testing.T) {
	const text = "uno due tre quattro cinque sei sette otto nove, " +
		"dieci undici dodici tredici quattordici quindici sedici diciassette diciotto; " +
		"diciannove venti ventuno ventidue ventitré ventiquattro venticinque ventisei ventisette."
	chunks := splitVoiceoverWords(text, 10)
	if len(chunks) != 3 {
		t.Fatalf("got %d chunks, want 3: %q", len(chunks), chunks)
	}
	wantEnd := []string{"nove,", "diciotto;", "ventisette."}
	for i, chunk := range chunks {
		fields := strings.Fields(chunk)
		if len(fields) != 9 {
			t.Fatalf("chunk %d has %d words, want 9: %q", i, len(fields), chunk)
		}
		if !strings.HasSuffix(chunk, wantEnd[i]) {
			t.Fatalf("chunk %d must end at clause %q, got %q", i, wantEnd[i], chunk)
		}
	}
}

func TestSplitVoiceoverWordsHardSplitsUnpunctuatedSentence(t *testing.T) {
	words := make([]string, 25)
	for i := range words {
		words[i] = "parola"
	}
	chunks := splitVoiceoverWords(strings.Join(words, " "), 10)
	wantSizes := []int{10, 10, 5}
	if len(chunks) != len(wantSizes) {
		t.Fatalf("got %d chunks, want %d", len(chunks), len(wantSizes))
	}
	for i, chunk := range chunks {
		if got := len(strings.Fields(chunk)); got != wantSizes[i] {
			t.Fatalf("chunk %d has %d words, want %d", i, got, wantSizes[i])
		}
	}
}

func TestVoiceoverChunksAParagraphSceneWithoutSplittingSentences(t *testing.T) {
	const sentences = 6
	text := voiceoverTestSentences(sentences, 100)
	chunks := splitVoiceoverWords(text, voiceoverChunkMaxWords)
	if len(chunks) != sentences {
		t.Fatalf("got %d chunks, want %d", len(chunks), sentences)
	}
	for i, chunk := range chunks {
		if got := len(strings.Fields(chunk)); got != 100 {
			t.Fatalf("chunk %d has %d words, want 100", i, got)
		}
		if !strings.HasSuffix(chunk, "fine.") {
			t.Fatalf("chunk %d does not end on a sentence boundary: %q", i, chunk)
		}
	}
}

func voiceoverTestSentence(words int) string {
	if words < 2 {
		return "fine."
	}
	return strings.Repeat("parola ", words-1) + "fine."
}

func voiceoverTestSentences(sentences, words int) string {
	parts := make([]string, sentences)
	for i := range parts {
		parts[i] = voiceoverTestSentence(words)
	}
	return strings.Join(parts, " ")
}

// Paragraph-sized chunks: test scenes are built from complete sentences at the
// production cap so the fan-out count stays deterministic.
func TestChunkedTTSProviderParallelismAndMergeOrder(t *testing.T) {
	provider := &recordingTTS{delay: 20 * time.Millisecond}
	merger := &recordingMerger{}
	chunker := &chunkedTTSProvider{inner: provider, merger: merger, concurrency: 4}
	text := voiceoverTestSentences(4, voiceoverChunkMaxWords) // 4 complete-sentence chunks
	_, err := chunker.Synthesize(context.Background(), voiceover.TTSInput{
		Text: text, Filename: "final.mp3", OutputDir: t.TempDir(), Language: "it",
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider.calls != 4 {
		t.Fatalf("calls=%d want=4", provider.calls)
	}
	if provider.maxActive < 2 || provider.maxActive > 4 {
		t.Fatalf("max parallelism=%d want 2..4", provider.maxActive)
	}
	for i, name := range merger.inputs {
		want := "final.chunk-00" + strconv.Itoa(i+1) + ".mp3"
		if name != want {
			t.Fatalf("merge input %d=%q want=%q", i, name, want)
		}
	}
}

func TestChunkedTTSProviderKeepsConfiguredConcurrencyImmutable(t *testing.T) {
	provider := &recordingTTS{delay: 5 * time.Millisecond}
	merger := &recordingMerger{}
	chunker := &chunkedTTSProvider{inner: provider, merger: merger, concurrency: 4}

	_, err := chunker.Synthesize(context.Background(), voiceover.TTSInput{
		// Three chunks exercise the per-request cap without requiring the
		// provider to rewrite its shared configured concurrency.
		Text: voiceoverTestSentences(3, voiceoverChunkMaxWords), Filename: "final.mp3", OutputDir: t.TempDir(), Language: "it",
	})
	if err != nil {
		t.Fatal(err)
	}
	if chunker.concurrency != 4 {
		t.Fatalf("configured concurrency=%d want=4", chunker.concurrency)
	}
	if provider.maxActive != 3 {
		t.Fatalf("max parallelism=%d want=3 chunks", provider.maxActive)
	}
}

// TestChunkedTTSProviderFailureDoesNotMerge pins: a failed chunk must never
// reach the merger. With the 2026-10-29 fail-open the whole-scene fallback
// succeeds instead (next test); when the fallback also fails the synthesis
// fails outright and still skips the merge.
func TestChunkedTTSProviderFailureDoesNotMerge(t *testing.T) {
	provider := &recordingTTS{failChunk: 2, failWholeScene: true}
	merger := &recordingMerger{}
	chunker := &chunkedTTSProvider{inner: provider, merger: merger, concurrency: 3}
	_, err := chunker.Synthesize(context.Background(), voiceover.TTSInput{
		Text: voiceoverTestSentences(3, voiceoverChunkMaxWords), Filename: "final.mp3", OutputDir: t.TempDir(), Language: "it",
	})
	if err == nil {
		t.Fatal("expected failed synthesis")
	}
	if merger.calls != 0 {
		t.Fatalf("merge calls=%d want=0", merger.calls)
	}
}

// TestChunkedTTSProviderFallsBackToWholeSceneOnChunkFailure pins the fail-open:
// one failed sentence chunk must degrade to the canonical single-request
// whole-scene synthesis (never fail the scene), with no merge of partial
// chunk state and no boundaries attached from the failed batch.
func TestChunkedTTSProviderFallsBackToWholeSceneOnChunkFailure(t *testing.T) {
	provider := &recordingTTS{failChunk: 2}
	merger := &recordingMerger{}
	chunker := &chunkedTTSProvider{inner: provider, merger: merger, concurrency: 3}
	outputDir := t.TempDir()
	out, err := chunker.Synthesize(context.Background(), voiceover.TTSInput{
		Text: voiceoverTestSentences(3, voiceoverChunkMaxWords), Filename: "final.mp3", OutputDir: outputDir, Language: "it",
	})
	if err != nil {
		t.Fatalf("fail-open fallback must succeed: %v", err)
	}
	if provider.fallbackCalls != 1 {
		t.Fatalf("fallback calls=%d want=1 (whole scene)", provider.fallbackCalls)
	}
	if merger.calls != 0 {
		t.Fatalf("merge calls=%d want=0 (no partial chunk merge)", merger.calls)
	}
	if file, statErr := os.Stat(filepath.Join(out.LocalPath)); file == nil || statErr != nil {
		t.Fatalf("fallback output missing: %v", statErr)
	}
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		t.Fatalf("read output dir: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".chunk-") {
			t.Errorf("failed fan-out leaked temporary chunk artifact %q", entry.Name())
		}
	}
}

// TestChunkedTTSProviderMergesWordBoundariesWithOffset pins the regression
// fix: per-chunk WordBoundaries must be merged into the merged track with a
// global offset remap (each chunk shifts by the cumulative duration of the
// preceding chunks). Without the remap, timing.mode=required fails closed
// (VOICEOVER_TIMING_UNAVAILABLE) on every multi-chunk scene.
func TestChunkedTTSProviderMergesWordBoundariesWithOffset(t *testing.T) {
	provider := &boundaryRecordingTTS{}
	merger := &recordingMerger{}
	chunker := &chunkedTTSProvider{inner: provider, merger: merger, concurrency: 2}

	out, err := chunker.Synthesize(context.Background(), voiceover.TTSInput{
		Text:     voiceoverTestSentences(3, voiceoverChunkMaxWords), // → 3 complete-sentence chunks
		Filename: "final.mp3", OutputDir: t.TempDir(), Language: "it",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Each chunk reports one boundary at start_us=1000/end_us=2000 with
	// duration 1s, so the merged offsets are 0, 1s, 2s in microsecond ticks.
	want := []voiceover.RawSpeechBoundary{
		{Text: "w0", StartUS: 1_000, EndUS: 2_000},
		{Text: "w1", StartUS: 1_001_000, EndUS: 1_002_000},
		{Text: "w2", StartUS: 2_001_000, EndUS: 2_002_000},
	}
	if len(out.WordBoundaries) != len(want) {
		t.Fatalf("merged boundaries=%d want=%d", len(out.WordBoundaries), len(want))
	}
	for i, b := range out.WordBoundaries {
		if b.Text != want[i].Text || b.StartUS != want[i].StartUS || b.EndUS != want[i].EndUS {
			t.Errorf("boundary[%d]=%+v want=%+v", i, b, want[i])
		}
	}
	if out.BoundaryMode != audio.BoundaryWord {
		t.Errorf("boundary mode=%q want=%q", out.BoundaryMode, audio.BoundaryWord)
	}
}

// boundaryRecordingTTS reports one deterministic word boundary per chunk
// (derived from the chunk index in the filename) with a 1s duration, so the
// merged boundary offsets are fully predictable.
type boundaryRecordingTTS struct{}

func (r *boundaryRecordingTTS) Synthesize(_ context.Context, in voiceover.TTSInput) (voiceover.TTSOutput, error) {
	base := filepath.Base(in.Filename)
	path := filepath.Join(in.OutputDir, base)
	if err := os.WriteFile(path, []byte(base), 0600); err != nil {
		return voiceover.TTSOutput{}, err
	}
	idx := 0
	if i := strings.Index(base, ".chunk-"); i >= 0 {
		n, err := strconv.Atoi(strings.TrimSuffix(base[i+len(".chunk-"):], filepath.Ext(base)))
		if err == nil {
			idx = n - 1
		}
	}
	return voiceover.TTSOutput{
		LocalPath:    path,
		Voice:        "fake",
		Duration:     time.Second,
		Provider:     "edge-tts",
		BoundaryMode: audio.BoundaryWord,
		WordBoundaries: []voiceover.RawSpeechBoundary{
			{Text: fmt.Sprintf("w%d", idx), StartUS: 1_000, EndUS: 2_000},
		},
	}, nil
}

type recordingTTS struct {
	mu             sync.Mutex
	active         int
	maxActive      int
	calls          int
	delay          time.Duration
	failChunk      int
	failWholeScene bool
	fallbackCalls  int
}

func (r *recordingTTS) Synthesize(_ context.Context, in voiceover.TTSInput) (voiceover.TTSOutput, error) {
	r.mu.Lock()
	r.active++
	r.calls++
	if r.active > r.maxActive {
		r.maxActive = r.active
	}
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.active--; r.mu.Unlock() }()
	if r.delay > 0 {
		time.Sleep(r.delay)
	}
	base := filepath.Base(in.Filename)
	if r.failChunk > 0 && strings.Contains(base, "chunk-003") {
		return voiceover.TTSOutput{}, os.ErrInvalid
	}
	if r.failWholeScene {
		r.mu.Lock()
		r.fallbackCalls++
		r.mu.Unlock()
		return voiceover.TTSOutput{}, os.ErrInvalid
	}
	if !strings.Contains(base, ".chunk-") {
		r.mu.Lock()
		r.fallbackCalls++
		r.mu.Unlock()
	}
	path := filepath.Join(in.OutputDir, base)
	if err := os.WriteFile(path, []byte(base), 0600); err != nil {
		return voiceover.TTSOutput{}, err
	}
	return voiceover.TTSOutput{LocalPath: path, Voice: "fake", Duration: time.Second}, nil
}

type recordingMerger struct {
	inputs []string
	calls  int
}

func (r *recordingMerger) MergeInputs(_ context.Context, inputs []string, output string) error {
	r.calls++
	for _, input := range inputs {
		r.inputs = append(r.inputs, filepath.Base(input))
	}
	return os.WriteFile(output, []byte(strings.Join(r.inputs, "\n")), 0600)
}
