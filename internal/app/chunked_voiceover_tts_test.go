package app

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

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
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

func TestChunkedTTSProviderParallelismAndMergeOrder(t *testing.T) {
	provider := &recordingTTS{delay: 20 * time.Millisecond}
	merger := &recordingMerger{}
	chunker := &chunkedTTSProvider{inner: provider, merger: merger, concurrency: 4}
	text := strings.Repeat("parola ", 1201)
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

func TestChunkedTTSProviderFailureDoesNotMerge(t *testing.T) {
	provider := &recordingTTS{failChunk: 2}
	merger := &recordingMerger{}
	chunker := &chunkedTTSProvider{inner: provider, merger: merger, concurrency: 3}
	_, err := chunker.Synthesize(context.Background(), voiceover.TTSInput{
		Text: strings.Repeat("parola ", 801), Filename: "final.mp3", OutputDir: t.TempDir(), Language: "it",
	})
	if err == nil {
		t.Fatal("expected failed chunk")
	}
	if merger.calls != 0 {
		t.Fatalf("merge calls=%d want=0", merger.calls)
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
		Text:     strings.Repeat("parola ", 801), // → 3 chunks of 400/400/1
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
	mu        sync.Mutex
	active    int
	maxActive int
	calls     int
	delay     time.Duration
	failChunk int
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
