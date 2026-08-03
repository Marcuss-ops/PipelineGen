package app

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
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
