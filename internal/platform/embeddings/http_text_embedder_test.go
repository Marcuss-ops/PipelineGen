package embeddings

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestHTTPTextEmbedderBatchPreservesOrderAndValidatesContract(t *testing.T) {
	texts := []string{"first", "second"}
	first := make([]float64, models.E5.Dimensions)
	second := make([]float64, models.E5.Dimensions)
	first[0], second[0] = 1, 2
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embed_batch" {
			t.Fatalf("path = %q, want /embed_batch", r.URL.Path)
		}
		var request struct {
			Texts []string `json:"texts"`
			Type  string   `json:"type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Type != "query" || len(request.Texts) != len(texts) || request.Texts[0] != texts[0] || request.Texts[1] != texts[1] {
			t.Fatalf("request = %+v, want ordered texts %v with query type", request, texts)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings":    [][]float64{first, second},
			"dimensions":    models.E5.Dimensions,
			"count":         len(texts),
			"model":         models.E5.ID,
			"model_version": models.E5.Revision,
			"contract_hash": coreembedding.CanonicalText.Hash(),
		})
	}))
	defer server.Close()

	results, err := NewHTTPTextEmbedder(server.URL).(*HTTPTextEmbedder).EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatalf("EmbedBatch returned error: %v", err)
	}
	if len(results) != len(texts) || results[0].Vector[0] != 1 || results[1].Vector[0] != 2 {
		t.Fatalf("batch results do not preserve order: first=%v second=%v", results[0].Vector[:1], results[1].Vector[:1])
	}
	if results[0].ContractHash != coreembedding.CanonicalText.Hash() || results[0].Dimensions != models.E5.Dimensions {
		t.Fatalf("batch provenance = %+v, want canonical contract", results[0])
	}
}

func TestHTTPTextEmbedderBatchFallsBackWhenSidecarIsNotUpgraded(t *testing.T) {
	var batchRequests, singleRequests int
	vector := make([]float64, models.E5.Dimensions)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/embed_batch":
			batchRequests++
			http.NotFound(w, r)
		case "/embed":
			singleRequests++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"embedding": vector, "dimensions": models.E5.Dimensions,
				"model": models.E5.ID, "model_version": models.E5.Revision,
				"contract_hash": coreembedding.CanonicalText.Hash(),
			})
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	results, err := NewHTTPTextEmbedder(server.URL).(*HTTPTextEmbedder).EmbedBatch(context.Background(), []string{"one", "two"})
	if err != nil {
		t.Fatalf("EmbedBatch on legacy sidecar: %v", err)
	}
	if len(results) != 2 || batchRequests != 1 || singleRequests != 2 {
		t.Fatalf("results=%d batch_requests=%d single_requests=%d, want 2, 1, 2", len(results), batchRequests, singleRequests)
	}
}

func TestHTTPTextEmbedderBatchRejectsCountAndDimensionDrift(t *testing.T) {
	for _, tc := range []struct {
		name      string
		response  map[string]any
		wantError string
	}{
		{
			name: "count mismatch",
			response: map[string]any{"embeddings": [][]float64{make([]float64, models.E5.Dimensions)}, "dimensions": models.E5.Dimensions,
				"count": 1, "model": models.E5.ID, "model_version": models.E5.Revision, "contract_hash": coreembedding.CanonicalText.Hash()},
			wantError: "count mismatch",
		},
		{
			name: "dimension mismatch",
			response: map[string]any{"embeddings": [][]float64{make([]float64, 1), make([]float64, 1)}, "dimensions": 1,
				"count": 2, "model": models.E5.ID, "model_version": models.E5.Revision, "contract_hash": coreembedding.CanonicalText.Hash()},
			wantError: "query_embedder diverged",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(tc.response)
			}))
			defer server.Close()
			_, err := NewHTTPTextEmbedder(server.URL).(*HTTPTextEmbedder).EmbedBatch(context.Background(), []string{"a", "b"})
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wantError)) {
				t.Fatalf("EmbedBatch error = %v, want %q", err, tc.wantError)
			}
		})
	}
}

func TestHTTPTextEmbedderBatchRejectsInvalidInputBeforeRequest(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests++ }))
	defer server.Close()
	embedder := NewHTTPTextEmbedder(server.URL).(*HTTPTextEmbedder)
	if results, err := embedder.EmbedBatch(context.Background(), nil); err != nil || len(results) != 0 {
		t.Fatalf("empty batch = %v, %v; want empty results without error", results, err)
	}
	for _, texts := range [][]string{{""}, make([]string, 33)} {
		if _, err := embedder.EmbedBatch(context.Background(), texts); err == nil {
			t.Fatalf("EmbedBatch(%d texts) unexpectedly succeeded", len(texts))
		}
	}
	if requests != 0 {
		t.Fatalf("invalid input sent %d HTTP requests, want 0", requests)
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
