package texttracks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	ollama "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
	"go.uber.org/zap"
)

// TestCueTranslationFanoutIsBoundedByOllamaAdmissionBudget is the end-to-end
// half of the translation-bottleneck fix: it runs the REAL cue fan-out against a
// stub Ollama that records how many requests it is serving at once.
//
// The property is that the pool's own width is not the real bound any more. The
// fan-out is configured WIDER (DefaultCueTranslationConcurrency) than the
// endpoint budget on purpose: the server must still never see more than the
// budget, because a wider pool against a 3-slot server only builds a queue
// inside Ollama that starves every other pool.
func TestCueTranslationFanoutIsBoundedByOllamaAdmissionBudget(t *testing.T) {
	const budget = 2
	t.Setenv("VELOX_OLLAMA_MAX_INFLIGHT", "2")

	var inFlight, peak atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := inFlight.Add(1)
		for {
			observed := peak.Load()
			if current <= observed || peak.CompareAndSwap(observed, current) {
				break
			}
		}
		defer inFlight.Add(-1)

		// Long enough that the fan-out would visibly overlap if it were
		// allowed to.
		time.Sleep(25 * time.Millisecond)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"role": "assistant", "content": "tradotto"},
			"done":    true,
		})
	}))
	defer server.Close()

	ollamaClient := client.NewClient(server.URL, "gemma4:e4b", 30)
	translator := translation.NewOllamaTranslator(ollama.NewGenerator(ollamaClient), zap.NewNop())

	const cueCount = 12
	cues := make([]detail.TimedCue, cueCount)
	for i := range cues {
		cues[i] = detail.TimedCue{
			StartMs: int64(i * 1000),
			EndMs:   int64(i*1000 + 800),
			Text:    fmt.Sprintf("source cue %d", i),
		}
	}

	translatorUnderTest := NewCueTranslator(
		translator, "en", "gemma4:e4b", DefaultCueTranslationConcurrency, zap.NewNop(),
	)
	// Per-cue fan-out: this is the path the batching fix did NOT remove, so it
	// must be bounded by the shared budget rather than by its own width.
	translatorUnderTest.SetBatchChunkSize(0)

	translated, stats, err := translatorUnderTest.Translate(context.Background(), cues, "it")
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if len(translated) != cueCount {
		t.Fatalf("translated cues = %d, want %d", len(translated), cueCount)
	}
	for i, cue := range translated {
		if cue.Text != "tradotto" {
			t.Fatalf("cue %d text = %q, want %q", i, cue.Text, "tradotto")
		}
		if cue.StartMs != cues[i].StartMs || cue.EndMs != cues[i].EndMs {
			t.Fatalf("cue %d timing = %d-%d, want %d-%d (translation must not move timings)",
				i, cue.StartMs, cue.EndMs, cues[i].StartMs, cues[i].EndMs)
		}
	}

	if got := peak.Load(); got > budget {
		t.Fatalf("stub Ollama saw %d concurrent requests, want <= %d (the pool width must not become the server load)", got, budget)
	}
	if stats.Configured != DefaultCueTranslationConcurrency {
		t.Fatalf("stats.Configured = %d, want %d", stats.Configured, DefaultCueTranslationConcurrency)
	}
	// The fan-out tracker measures TASK overlap, and a task that is blocked on
	// the client's admission budget still counts as running: it cannot see
	// inside the client (that is by design — the pool sizes its own work, the
	// client owns the server ceiling). So it may legitimately report the full
	// pool width while the server sees only the budget. Its queue accounting
	// stays blind to the in-client wait too, which is exactly why the client
	// exposes AdmissionStats: that is the source of truth for how many requests
	// the server is actually running.
	if stats.MaxObserved > DefaultCueTranslationConcurrency {
		t.Fatalf("stats.MaxObserved = %d, want <= the fan-out width %d", stats.MaxObserved, DefaultCueTranslationConcurrency)
	}

	admission := ollamaClient.AdmissionStats()
	if admission.Limit != budget {
		t.Fatalf("client limit = %d, want %d", admission.Limit, budget)
	}
	if admission.Peak > int64(budget) {
		t.Fatalf("client peak = %d, want <= %d", admission.Peak, budget)
	}
	if admission.Deferred == 0 {
		t.Fatal("client Deferred = 0: a wider pool against a narrower budget must queue")
	}
}
