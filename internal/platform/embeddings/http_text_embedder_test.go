package embeddings

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/models"
)

func TestHTTPTextEmbedderAcceptsCanonicalRegistryResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embed" {
			t.Fatalf("path = %q, want /embed", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embedding":     make([]float64, models.E5.Dimensions),
			"dimensions":    models.E5.Dimensions,
			"model":         models.E5.ID,
			"model_version": models.E5.Revision,
			"contract_hash": coreembedding.CanonicalText.Hash(),
		})
	}))
	defer server.Close()

	result, err := NewHTTPTextEmbedder(server.URL).Embed(context.Background(), "canonical query")
	if err != nil {
		t.Fatalf("Embed returned error: %v", err)
	}
	if len(result.Vector) != models.E5.Dimensions {
		t.Fatalf("vector dimensions = %d, want %d", len(result.Vector), models.E5.Dimensions)
	}
}

func TestHTTPTextEmbedderRejectsRegistryDrift(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embedding":     make([]float64, models.E5.Dimensions),
			"dimensions":    models.E5.Dimensions,
			"model":         "nomic-embed-text",
			"model_version": models.E5.Revision,
			"contract_hash": coreembedding.CanonicalText.Hash(),
		})
	}))
	defer server.Close()

	_, err := NewHTTPTextEmbedder(server.URL).Embed(context.Background(), "drifted query")
	if err == nil {
		t.Fatal("Embed must reject a response from a non-canonical model")
	}
	if !errors.Is(err, coreembedding.ErrContractMismatch) {
		t.Fatalf("error = %v, want ErrContractMismatch", err)
	}
}
