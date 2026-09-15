package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

func TestGenerateDetailedPropagatesKeepAliveAndTiming(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":"ok","done":true,"load_duration":120000000,"prompt_eval_count":4,"prompt_eval_duration":80000000,"eval_count":8,"eval_duration":200000000,"total_duration":420000000}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "gemma4:e4b", 5)
	options := map[string]any{"keep_alive": "45m", "num_predict": 8, "think": false}
	result, err := c.GenerateDetailed(t.Context(), "gemma4:e4b", "hello", options)
	if err != nil {
		t.Fatalf("GenerateDetailed: %v", err)
	}
	if result.Content != "ok" {
		t.Fatalf("content = %q, want ok", result.Content)
	}
	if result.Metrics == nil || result.Metrics.ModelLoadMS() != 120 || result.Metrics.TokensPerSecond() != 40 {
		t.Fatalf("unexpected metrics: %#v", result.Metrics)
	}
	if got, ok := body["keep_alive"].(string); !ok || got != "45m" {
		t.Fatalf("top-level keep_alive = %#v, want 45m", body["keep_alive"])
	}
	if _, nested := body["options"].(map[string]any)["keep_alive"]; nested {
		t.Fatal("keep_alive must not be nested in options")
	}
	if _, nested := body["options"].(map[string]any)["think"]; nested {
		t.Fatal("think must not be nested in options")
	}
	if options["keep_alive"] != "45m" || options["think"] != false {
		t.Fatalf("caller options mutated: %#v", options)
	}
}

// TestGenerateDetailedPinsSingleResidentRunnerContext pins the single-runner
// invariant on the legacy /api/generate surface. A request without num_ctx made
// Ollama fall back to its own default and rebuild the model the warm-up had
// just paid for, so entity/phrase extraction re-loaded it inside the run.
func TestGenerateDetailedPinsSingleResidentRunnerContext(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":"ok","done":true}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "gemma4:e4b", 5)

	// Caller omits num_ctx (the entity/phrase extraction shape): pinned.
	callerOptions := map[string]any{"num_predict": 256, "temperature": 0}
	if _, err := c.GenerateDetailed(t.Context(), "gemma4:e4b", "hello", callerOptions); err != nil {
		t.Fatalf("GenerateDetailed: %v", err)
	}
	if got := body["options"].(map[string]any)["num_ctx"]; got != float64(types.ProductionRunnerContext) {
		t.Fatalf("num_ctx = %#v, want %d (single resident runner)", got, types.ProductionRunnerContext)
	}
	if _, mutated := callerOptions["num_ctx"]; mutated {
		t.Fatalf("caller options mutated: %#v", callerOptions)
	}

	// Nil options: still pinned, never the Ollama default.
	if _, err := c.GenerateDetailed(t.Context(), "gemma4:e4b", "hello", nil); err != nil {
		t.Fatalf("GenerateDetailed(nil): %v", err)
	}
	if got := body["options"].(map[string]any)["num_ctx"]; got != float64(types.ProductionRunnerContext) {
		t.Fatalf("nil-options num_ctx = %#v, want %d", got, types.ProductionRunnerContext)
	}

	// An explicit context stays authoritative (intentional opt-out).
	if _, err := c.GenerateDetailed(t.Context(), "gemma4:e4b", "hello", map[string]any{"num_ctx": 2048}); err != nil {
		t.Fatalf("GenerateDetailed(explicit): %v", err)
	}
	if got := body["options"].(map[string]any)["num_ctx"]; got != float64(2048) {
		t.Fatalf("explicit num_ctx = %#v, want 2048", got)
	}
}

func TestGenerateDetailedUsesResidentDefaultKeepAlive(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":"ok","done":true}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "gemma4:e4b", 5)
	if _, err := c.GenerateDetailed(t.Context(), "", "hello", nil); err != nil {
		t.Fatalf("GenerateDetailed: %v", err)
	}
	if got, ok := body["keep_alive"].(string); !ok || got != "30m" {
		t.Fatalf("default keep_alive = %#v, want 30m", body["keep_alive"])
	}
}
