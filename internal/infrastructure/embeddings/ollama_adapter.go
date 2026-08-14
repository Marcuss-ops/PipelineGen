// Package embeddings — OllamaEmbedderAdapter wraps the legacy
// *ollamaClient.Client as a canonical asset.Embedder.
//
// QDRANT-001b (July 2026): before the Embedder interface was promoted from
// ([]float32, error) to (EmbeddingResult, error), *client.Client satisfied
// the old signature directly through its Embed method. After the promotion,
// the client no longer matches the new return type. This adapter bridges
// the gap: it calls client.Embed(ctx, text) and wraps the []float32 result
// into an EmbeddingResult with Model="" and ModelVersion="" (the ollama
// client does not expose model provenance in its API response).
//
// The adapter lives here rather than in the ai/ollama/client/ package
// because embeddings/ is the canonical home for infrastructure-level
// Embedder implementations and already owns PythonScriptEmbedder +
// HTTPTextEmbedder.
package embeddings

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// OllamaEmbedderAdapter wraps *client.Client to satisfy asset.Embedder.
// Model provenance is owned by the composition root/schema contract. The
// Ollama wire response is not trusted to redefine the active vector space.
type OllamaEmbedderAdapter struct {
	client *client.Client
}

// Dimensions is inferred from the vector length because the Ollama
// /api/embeddings endpoint does not return explicit dimension metadata.
//
// NewOllamaEmbedderAdapter creates an asset.Embedder from an ollama client.
// Returns nil when client is nil (callers should check before wiring).
func NewOllamaEmbedderAdapter(client *client.Client) coreembedding.Embedder {
	if client == nil {
		return nil
	}
	return &OllamaEmbedderAdapter{client: client}
}

// Compile-time assertion: OllamaEmbedderAdapter satisfies asset.Embedder.
var _ coreembedding.Embedder = (*OllamaEmbedderAdapter)(nil)

// Embed delegates to client.Client.Embed and wraps the []float32 vector
// into an EmbeddingResult. Model and ModelVersion are empty (the Ollama
// API does not return provenance metadata). Empty text short-circuits
// to (EmbeddingResult{}, nil).
func (a *OllamaEmbedderAdapter) Embed(ctx context.Context, text string) (coreembedding.EmbeddingResult, error) {
	if text == "" {
		return coreembedding.EmbeddingResult{}, nil
	}

	vec, err := a.client.Embed(ctx, text)
	if err != nil {
		return coreembedding.EmbeddingResult{}, err
	}

	return coreembedding.EmbeddingResult{
		Vector:       vec,
		Dimensions:   len(vec),
		Model:        "",
		ModelVersion: "",
		ContractHash: "",
	}, nil
}
