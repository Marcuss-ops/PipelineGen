// Package embeddings — HTTPTextEmbedder is the second concrete Embedder
// implementation (alongside PythonScriptEmbedder in python.go) extracted
// in PR-D.5.2. This wraps a Python sidecar embedding server (started
// by clipindexer/server.py) and exposes a text-only Embedder for
// callers that only need the canonical Embed(ctx, text) ([]float32, error)
// contract.
//
// The existing application/realtime.PythonEmbeddingAdapter stays in place
// for now because it serves multipurpose calls beyond text embedding
// (EmbedVisual, EmbedAudio, AnalyzeImage, normalized-text return).
// Migrating those callers wholesale is deferred to PR-D.5.2b; this
// file establishes the pattern and a concrete http.TextEmbedder for
// any consumer that only needs the canonical interface.
package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/core/embedding"
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
// to (nil, nil) to match the canonical contract.
//
// Error wrapping includes the original HTTP status code and body so
// production observability can correlate embedder failures with
// Qdrant upsert health.
func (e *HTTPTextEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, nil
	}

	payload, err := json.Marshal(map[string]string{
		"text": text,
		"type": "query", // E5 model prefix for queries (vs "passage" for index)
	})
	if err != nil {
		return nil, fmt.Errorf("marshal embedder request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.serverURL+"/embed", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create embedder request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedder request failed: %w", err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("read embedder response: %w", readErr)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedder returned status %d: %s", resp.StatusCode, string(body))
	}

	var parsed struct {
		Embedding []float64 `json:"embedding"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse embedder response: %w (body: %s)", err, string(body))
	}

	out := make([]float32, len(parsed.Embedding))
	for i, v := range parsed.Embedding {
		out[i] = float32(v)
	}
	return out, nil
}
