package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

// TestResidentModelPinsGenerationRequest proves WST-02: with a pinned resident
// model, a payload asking another size reaches the wire as the resident one, so
// the single loaded runner is never evicted.
func TestResidentModelPinsGenerationRequest(t *testing.T) {
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model   string         `json:"model"`
			Options map[string]any `json:"options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode chat request: %v", err)
		}
		seen = body.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"Testo breve"},"done":true}`))
	}))
	defer server.Close()

	gen := NewGenerator(client.NewClient(server.URL, "gemma4:e2b", 5))
	gen.SetResidentModel("gemma4:e2b")
	result, err := gen.GenerateScript(context.Background(), types.TextGenerationRequest{
		Model: "gemma4:e4b", Language: "it", Title: "test", Prompt: "scrivi una frase",
		SourceText: "testo", MaxChars: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen != "gemma4:e2b" {
		t.Fatalf("chat model=%q, want the pinned resident model", seen)
	}
	if result.Model != "gemma4:e2b" {
		t.Fatalf("result model=%q, want the effective (pinned) model", result.Model)
	}
	if got := gen.ResidentPinnedRequests(); got != 1 {
		t.Fatalf("pinned requests=%d, want 1", got)
	}
}

// TestResidentModelPinsTranslationRequest proves the policy covers the
// translation leg too: a per-request translation model cannot evict the runner.
func TestResidentModelPinsTranslationRequest(t *testing.T) {
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode chat request: %v", err)
		}
		seen = body.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ciao"},"done":true}`))
	}))
	defer server.Close()

	gen := NewGenerator(client.NewClient(server.URL, "gemma4:e2b", 5))
	gen.SetResidentModel("gemma4:e2b")
	if _, err := gen.TranslateTextWithModel(context.Background(), "hello", "it", "gemma4:e4b"); err != nil {
		t.Fatal(err)
	}
	if seen != "gemma4:e2b" {
		t.Fatalf("translation model=%q, want the pinned resident model", seen)
	}
}

// TestResidentModelUnsetKeepsPerCallOverride pins the rollback contract: with no
// resident model the historical per-call override is still forwarded verbatim.
func TestResidentModelUnsetKeepsPerCallOverride(t *testing.T) {
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode chat request: %v", err)
		}
		seen = body.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"Testo breve"},"done":true}`))
	}))
	defer server.Close()

	gen := NewGenerator(client.NewClient(server.URL, "gemma4:e2b", 5))
	if _, err := gen.GenerateScript(context.Background(), types.TextGenerationRequest{
		Model: "gemma4:e4b", Language: "it", Title: "test", Prompt: "scrivi una frase",
		SourceText: "testo", MaxChars: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if seen != "gemma4:e4b" {
		t.Fatalf("chat model=%q, want the explicit request model when no resident model is pinned", seen)
	}
}
