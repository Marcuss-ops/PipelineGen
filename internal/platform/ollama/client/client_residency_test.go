package client

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

// TestResidentRunnerOptionsPinsContextWhenOmitted is the reload guard: a call
// site that omits num_ctx must inherit the production bucket instead of
// letting Ollama rebuild the runner with its own default.
func TestResidentRunnerOptionsPinsContextWhenOmitted(t *testing.T) {
	input := map[string]any{"temperature": 0.2}
	pinned := residentRunnerOptions(input)
	if got := pinned["num_ctx"]; got != types.ProductionRunnerContext {
		t.Fatalf("num_ctx = %v, want %d", got, types.ProductionRunnerContext)
	}
	if _, mutated := input["num_ctx"]; mutated {
		t.Fatal("residentRunnerOptions must not mutate the caller's map")
	}
	explicit := residentRunnerOptions(map[string]any{"num_ctx": 4096})
	if got := explicit["num_ctx"]; got != 4096 {
		t.Fatalf("explicit num_ctx = %v, want 4096 (caller stays authoritative)", got)
	}
}

// TestModelResidencyReportsConfiguredModel pins the readiness fact: the
// configured model is reported with its residency at the production context.
func TestModelResidencyReportsConfiguredModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ps" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"models":[{"name":"gemma4:e4b","context_length":%d}]}`, types.ProductionRunnerContext)
	}))
	defer server.Close()

	c := NewClient(server.URL, "gemma4:e4b", 5)
	model, resident, err := c.ModelResidency(context.Background())
	if err != nil {
		t.Fatalf("ModelResidency: %v", err)
	}
	if model != "gemma4:e4b" || !resident {
		t.Fatalf("ModelResidency = %q/%v, want the configured model resident", model, resident)
	}
}

// TestModelResidencyNilClientIsNotAConfiguredModel keeps the probe nil-safe.
func TestModelResidencyNilClientIsNotAConfiguredModel(t *testing.T) {
	var c *Client
	model, resident, err := c.ModelResidency(context.Background())
	if err != nil || model != "" || resident {
		t.Fatalf("nil client = %q/%v/%v, want empty/false/nil", model, resident, err)
	}
}
