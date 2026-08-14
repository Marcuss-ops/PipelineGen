package embeddings

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
)

func TestContractProbe_Fetch_ParsesCanonicalContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/contract" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"contract_version": "v1",
			"model_id": "intfloat/multilingual-e5-base",
			"model_revision": "2026-06-26-v1",
			"dimension": 768,
			"normalization": "l2",
			"distance": "Cosine",
			"query_prefix": "query: ",
			"document_prefix": "passage: ",
			"semantic_document_version": "v3"
		}`))
	}))
	defer server.Close()

	probe := NewContractProbe(server.URL)
	got, err := probe.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if !got.Equal(coreembedding.CanonicalText) {
		t.Fatalf("Fetch() = %+v, want canonical %+v", got, coreembedding.CanonicalText)
	}
}

func TestContractProbe_Fetch_404FailsClosed(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	probe := NewContractProbe(server.URL)
	_, err := probe.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected fail-closed error on missing /contract endpoint")
	}
	if !strings.Contains(err.Error(), "/contract") {
		t.Fatalf("error should mention /contract, got: %v", err)
	}
}

func TestContractProbe_Fetch_IncompleteContractFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"model_id": "", "dimension": 0}`))
	}))
	defer server.Close()

	probe := NewContractProbe(server.URL)
	_, err := probe.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected error for incomplete contract")
	}
}

func TestContractProbe_Fetch_UnconfiguredFails(t *testing.T) {
	probe := NewContractProbe("")
	if _, err := probe.Fetch(context.Background()); err == nil {
		t.Fatal("expected error when sidecar URL is empty")
	}
}
