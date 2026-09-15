// Package adapters — important_phrase_extractor_test.go pins the batched
// important-phrase adapter added by the script-generation latency pass.
//
// Three facts are load-bearing for the caller (scripts.runTranslatedNLP):
//
//  1. The composition-root constructor returns a value that satisfies the
//     OPTIONAL batched contract. The runner reaches the batched path through a
//     runtime type assertion, so a constructor that returned a non-batching
//     decorator would silently fall back to one model call per scene and the
//     batching win would disappear with no test failure.
//  2. No single request exceeds client.EntityExtractionBatchLimit. The
//     underlying client rejects an oversized batch, so chunking is the
//     adapter's contract, not the caller's.
//  3. The returned slice is aligned by INPUT POSITION across chunk boundaries.
//     A chunking bug that dropped or reordered a block would attach one scene's
//     phrases to another scene's annotations.
package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
)

// batchPromptInputRE extracts one `SEGMENT_INPUT_<n>:\n<text>` block from the
// batch prompt, so the fake model answers about exactly the segments it was
// given (and echoes each segment's own marker phrase back).
var batchPromptInputRE = regexp.MustCompile(`SEGMENT_INPUT_(\d+):\n([^\n]*)`)

// phraseForSegment derives a verbatim, distinctive phrase from a test segment.
// The phrase must appear in the segment text because the client grounds every
// returned phrase against its own segment before the adapter ever sees it.
func phraseForSegment(segment string) string {
	digits := ""
	for _, r := range segment {
		if r >= '0' && r <= '9' {
			digits += string(r)
		}
	}
	if digits == "" {
		return "alpha bravo charlie"
	}
	return fmt.Sprintf("Alpha%s Bravo%s Charlie%s", digits, digits, digits)
}

func testSegment(i int) string {
	return fmt.Sprintf("Alpha%d Bravo%d Charlie%d delta segment body", i, i, i)
}

type phraseBatchRecorder struct {
	mu         sync.Mutex
	chunkSizes []int
}

func (r *phraseBatchRecorder) record(size int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chunkSizes = append(r.chunkSizes, size)
}

func (r *phraseBatchRecorder) sizes() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.chunkSizes...)
}

func TestOllamaImportantPhraseExtractorBatch(t *testing.T) {
	recorder := &phraseBatchRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		inputs := batchPromptInputRE.FindAllStringSubmatch(body.Prompt, -1)
		recorder.record(len(inputs))

		var blocks strings.Builder
		for i, match := range inputs {
			fmt.Fprintf(&blocks,
				"### SEGMENT_INDEX: %d\n## frasi_importanti\n- %s\n## entity_senza_testo\n## nomi_speciali\n## parole_importanti\n## artlist_phrases\n### END_SEGMENT\n",
				i, phraseForSegment(match[2]))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"response": blocks.String()})
	}))
	defer server.Close()

	extractor := NewOllamaImportantPhraseExtractor(client.NewClient(server.URL, "test-model", 5))
	batcher, ok := extractor.(scriptgen.BatchImportantPhraseExtractor)
	if !ok {
		t.Fatal("composition-root constructor does not satisfy BatchImportantPhraseExtractor; the runner would silently use one call per scene")
	}

	const total = 12
	texts := make([]string, total)
	for i := range texts {
		texts[i] = testSegment(i)
	}
	phrases, err := batcher.ExtractImportantPhrasesBatch(context.Background(), texts, 3, "en", "test-model")
	if err != nil {
		t.Fatalf("ExtractImportantPhrasesBatch: %v", err)
	}
	if len(phrases) != total {
		t.Fatalf("returned %d phrase lists for %d segments", len(phrases), total)
	}

	sizes := recorder.sizes()
	if want := []int{5, 5, 2}; len(sizes) != len(want) {
		t.Fatalf("chunk requests = %v, want %v (bounded by EntityExtractionBatchLimit)", sizes, want)
	} else {
		for i, size := range sizes {
			if size != want[i] {
				t.Fatalf("chunk requests = %v, want %v", sizes, want)
			}
		}
	}
	if limit := client.EntityExtractionBatchLimit; total > limit {
		for _, size := range sizes {
			if size > limit {
				t.Fatalf("request covered %d segments, over the %d bound", size, limit)
			}
		}
	}

	for i, got := range phrases {
		want := phraseForSegment(texts[i])
		found := false
		for _, phrase := range got {
			if phrase == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("segment %d phrases = %v, want to contain %q (positional alignment across chunks)", i, got, want)
		}
	}
}

func TestOllamaImportantPhraseExtractorBatchEmptyAndNil(t *testing.T) {
	var extractor *OllamaImportantPhraseExtractor
	if got, err := extractor.ExtractImportantPhrasesBatch(context.Background(), []string{"a"}, 3, "en", "m"); err != nil || got != nil {
		t.Fatalf("nil receiver = (%v, %v), want (nil, nil)", got, err)
	}
	wired := NewOllamaImportantPhraseExtractor(client.NewClient("http://127.0.0.1:1", "test-model", 5)).(scriptgen.BatchImportantPhraseExtractor)
	if got, err := wired.ExtractImportantPhrasesBatch(context.Background(), nil, 3, "en", "m"); err != nil || got != nil {
		t.Fatalf("empty input = (%v, %v), want (nil, nil) and no request", got, err)
	}
}
