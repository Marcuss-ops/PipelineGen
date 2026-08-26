package reranker

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/models"
)

func TestConfigDefaultsToCanonicalRegistryModel(t *testing.T) {
	got := (Config{}).WithDefaults()
	if got.Model != models.Reranker.ID {
		t.Fatalf("model = %q, want canonical registry model %q", got.Model, models.Reranker.ID)
	}
}

func TestClientDisablesNonCanonicalModel(t *testing.T) {
	client := NewClient(Config{Enabled: true, Model: "different/reranker"})
	if client.IsEnabled() {
		t.Fatal("client must be disabled when configured model differs from registry")
	}
}

func TestNewValidatedClientRejectsNonCanonicalModel(t *testing.T) {
	_, err := NewValidatedClient(Config{Enabled: true, Model: "different/reranker"})
	if err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("expected canonical-model validation error, got %v", err)
	}
}

func TestRerankRejectsIncompleteSidecarResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer server.Close()

	client, err := NewValidatedClient(Config{
		Enabled:   true,
		URL:       server.URL,
		Model:     models.Reranker.ID,
		TopK:      2,
		TimeoutMs: 1000,
		Weight:    0.35,
	})
	if err != nil {
		t.Fatalf("create validated client: %v", err)
	}
	_, err = client.Rerank(context.Background(), "query", []Candidate{
		{ID: "asset-1", Text: "candidate"},
	})
	if err == nil || !strings.Contains(err.Error(), "result count") {
		t.Fatalf("expected incomplete-response error, got %v", err)
	}
}

func TestValidateResultsRejectsUnknownAndNonFiniteScores(t *testing.T) {
	candidates := []Candidate{{ID: "asset-1", Text: "candidate"}}
	if err := validateResults(candidates, []Result{{ID: "other", RerankScore: 0.5}}); err == nil {
		t.Fatal("expected unknown result id to be rejected")
	}
	if err := validateResults(candidates, []Result{{ID: "asset-1", RerankScore: math.Inf(1)}}); err == nil {
		t.Fatal("expected non-finite score to be rejected")
	}
}
