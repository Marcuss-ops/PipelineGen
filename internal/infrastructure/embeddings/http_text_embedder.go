// Package embeddings — HTTPTextEmbedder is the second concrete Embedder
// implementation (alongside PythonScriptEmbedder in python.go) extracted
// in PR-D.5.2. This wraps a Python sidecar embedding server (started
// by clipindexer/server.py) and exposes a text-only Embedder for
// callers that only need the canonical Embed(ctx, text) ([]float32, error)
// contract.
//
// FASE 1.1 C10 closure (2026-07-04, 213e18a8 + amend): the historical
// application/realtime.PythonEmbeddingAdapter surface was retired in
// prior waves (canonical owner per godlike/06 SSOT is *ollama.Generator,
// fully wired across 39 call sites). The 2-line ghost-comment reference
// above is removed as part of PR-IMAGES-AI-VS-NORMAL-PLAN action C10
// (no production dependency on application/realtime.PythonEmbeddingAdapter
// remains after the retirement; the directional reference was an orphan
// godlike/06 residue-amendment fossil).
package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// HTTPTextEmbedder calls a Python embedding sidecar server's /embed
// endpoint and parses the returned []float64 back to []float32.
//
// The sidecar is a long-lived FastAPI server (scripts/bridges/
// vector_embedding_server.py or similar) that holds the E5 model in
// memory and amortises load cost across queries. Compared to the
// PythonScriptEmbedder (per-call subprocess startup), this trades
// deployment complexity for ~100x latency reduction on hot paths.
type HTTPTextEmbedder struct {
	serverURL  string
	httpClient *http.Client
}

// NewHTTPTextEmbedder creates an Embedder pointing at the given sidecar
// URL. The default 10-second timeout is appropriate for E5 inference
// (typically 50–200ms per query); tune in production if Qdrant-backed
// batch jobs need longer deadlines.
func NewHTTPTextEmbedder(serverURL string) coreembedding.Embedder {
	return &HTTPTextEmbedder{
		serverURL:  serverURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// Embed posts {"text": ..., "type": "query"} to the sidecar's /embed
// endpoint and returns the parsed embedding. Empty text short-circuits
// to (EmbeddingResult{}, nil) to match the canonical contract.
//
// QDRANT-001b (July 2026): the return type is now EmbeddingResult
// instead of []float32. The sidecar returns the canonical envelope
// {"embedding": [...], "dimensions": 768, "model": "<name>",
// "model_version": "<version>", "error": ""}. Graceful fallback: when
// the sidecar is not yet updated and returns only {"embedding": [...]},
// we set Model="" and ModelVersion="" (the trade-off
// documented in the QDRANT-001b closure ticket).
//
// Error wrapping includes the original HTTP status code and body so
// production observability can correlate embedder failures with
// Qdrant upsert health.
func (e *HTTPTextEmbedder) Embed(ctx context.Context, text string) (coreembedding.EmbeddingResult, error) {
	if text == "" {
		return coreembedding.EmbeddingResult{}, nil
	}

	payload, err := json.Marshal(map[string]string{
		"text": text,
		"type": "query", // E5 model prefix for queries (vs "passage" for index)
	})
	if err != nil {
		return coreembedding.EmbeddingResult{}, fmt.Errorf("marshal embedder request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.serverURL+"/embed", bytes.NewReader(payload))
	if err != nil {
		return coreembedding.EmbeddingResult{}, fmt.Errorf("create embedder request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return coreembedding.EmbeddingResult{}, fmt.Errorf("embedder request failed: %w", err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return coreembedding.EmbeddingResult{}, fmt.Errorf("read embedder response: %w", readErr)
	}
	if resp.StatusCode != http.StatusOK {
		return coreembedding.EmbeddingResult{}, fmt.Errorf("embedder returned status %d: %s", resp.StatusCode, string(body))
	}

	// QDRANT-001b: try canonical envelope first, fall back to legacy raw vector.
	var envelope struct {
		Embedding    []float64 `json:"embedding"`
		Dimensions   int       `json:"dimensions"`
		Model        string    `json:"model"`
		ModelVersion string    `json:"model_version"`
		ContractHash string    `json:"contract_hash"`
		Error        string    `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		// Fallback: legacy sidecar emitting only {"embedding": [...]}.
		var legacy struct {
			Embedding []float64 `json:"embedding"`
		}
		if err2 := json.Unmarshal(body, &legacy); err2 != nil {
			return coreembedding.EmbeddingResult{}, fmt.Errorf("parse embedder response: %w (body: %s)", err, string(body))
		}
		out := make([]float32, len(legacy.Embedding))
		for i, v := range legacy.Embedding {
			out[i] = float32(v)
		}
		return coreembedding.EmbeddingResult{
			Vector:     out,
			Dimensions: len(out),
		}, nil
	}

	if envelope.Error != "" {
		return coreembedding.EmbeddingResult{}, fmt.Errorf("sidecar error: %s", envelope.Error)
	}

	if len(envelope.Embedding) == 0 {
		return coreembedding.EmbeddingResult{}, fmt.Errorf("sidecar returned empty embedding vector")
	}

	// QDRANT-001b: validate declared dimensions match actual vector length.
	if envelope.Dimensions > 0 && envelope.Dimensions != len(envelope.Embedding) {
		return coreembedding.EmbeddingResult{}, fmt.Errorf(
			"dimension mismatch: declared %d, actual embedding length %d",
			envelope.Dimensions, len(envelope.Embedding))
	}

	out := make([]float32, len(envelope.Embedding))
	for i, v := range envelope.Embedding {
		out[i] = float32(v)
	}
	return coreembedding.EmbeddingResult{
		Vector:       out,
		Dimensions:   envelope.Dimensions,
		Model:        envelope.Model,
		ModelVersion: envelope.ModelVersion,
		ContractHash: envelope.ContractHash,
	}, nil
}
