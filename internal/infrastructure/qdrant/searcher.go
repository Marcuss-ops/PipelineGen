package qdrant

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// Searcher provides semantic search over Qdrant collections.
// It routes all searches through the runtime alias and validates vectors
// against the manifest.
type Searcher struct {
	client *Client
	schema *IndexSchema
	log    *zap.Logger
}

// NewSearcher creates a Searcher.
func NewSearcher(client *Client, schema *IndexSchema, log *zap.Logger) *Searcher {
	return &Searcher{
		client: client,
		schema: schema,
		log:    log,
	}
}

// Search performs an ANN dense vector search using the runtime alias.
func (s *Searcher) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	if req.Vector == nil {
		return nil, fmt.Errorf("search vector must not be nil")
	}
	if req.Limit <= 0 {
		req.Limit = 20
	}

	// Validate vector dimensions against the manifest.
	if req.VectorName != "" {
		spec := s.schema.GetDense(req.VectorName)
		if spec != nil && len(req.Vector) != spec.Dimensions {
			return nil, &ErrVectorDimensionMismatch{
				Channel:  req.VectorName,
				Expected: spec.Dimensions,
				Actual:   len(req.Vector),
				AssetID:  "(query)",
			}
		}
	}

	collection, err := s.resolveCollection(ctx)
	if err != nil {
		return nil, err
	}

	results, err := s.client.SearchPoints(ctx, collection, req)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	return results, nil
}

// HybridSearch performs a hybrid dense + sparse search.
func (s *Searcher) HybridSearch(ctx context.Context, req HybridSearchRequest) ([]SearchResult, error) {
	if req.DenseVector == nil {
		return nil, fmt.Errorf("dense vector must not be nil for hybrid search")
	}
	if req.Limit <= 0 {
		req.Limit = 20
	}

	// Validate dense vector dimensions.
	spec := s.schema.GetDense(req.DenseVectorName)
	if spec != nil && len(req.DenseVector) != spec.Dimensions {
		return nil, &ErrVectorDimensionMismatch{
			Channel:  req.DenseVectorName,
			Expected: spec.Dimensions,
			Actual:   len(req.DenseVector),
			AssetID:  "(query)",
		}
	}

	collection, err := s.resolveCollection(ctx)
	if err != nil {
		return nil, err
	}

	results, err := s.client.HybridSearchPoints(ctx, collection, req)
	if err != nil {
		return nil, fmt.Errorf("hybrid search: %w", err)
	}

	return results, nil
}

// SearchByText creates an embedding from text and performs an ANN search.
// The embedder is injected at construction time so the Searcher doesn't
// need to know about specific models.
func (s *Searcher) SearchByText(ctx context.Context, text string, embedder TextEmbedder, vectorName string, limit int, minScore float64) ([]SearchResult, error) {
	vec, err := embedder.Embed(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("embed query text: %w", err)
	}
	return s.Search(ctx, SearchRequest{
		Vector:     vec,
		VectorName: vectorName,
		Limit:      limit,
		MinScore:   minScore,
	})
}

func (s *Searcher) resolveCollection(ctx context.Context) (string, error) {
	collection, err := s.client.GetAliasTarget(ctx, s.schema.RuntimeAlias)
	if err != nil {
		return "", fmt.Errorf("resolve alias target: %w", err)
	}
	if collection == "" {
		return "", fmt.Errorf("runtime alias %q has no target — run EnsureSchema first", s.schema.RuntimeAlias)
	}
	return collection, nil
}

// ── Embedding contract ───────────────────────────────────────────────

// TextEmbedder is the narrow interface for text embedding.
// Implementations include HTTPTextEmbedder, OllamaClient, etc.
type TextEmbedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// ImageEmbedder generates visual embeddings from image data.
type ImageEmbedder interface {
	EmbedImages(ctx context.Context, imagePaths []string) ([][]float32, error)
}

// AudioEmbedder generates audio embeddings from audio data.
type AudioEmbedder interface {
	EmbedAudio(ctx context.Context, audioPaths []string) ([][]float32, error)
}

// ── Application-level search adapter ─────────────────────────────────

// SearchAdapter adapts the qdrant Searcher to the application-level
// search.VectorStorePort interface. This lives in the infrastructure
// layer because it depends directly on the qdrant package.
type SearchAdapter struct {
	searcher *Searcher
	log      *zap.Logger
}

// NewSearchAdapter creates a SearchAdapter.
func NewSearchAdapter(searcher *Searcher, log *zap.Logger) *SearchAdapter {
	return &SearchAdapter{searcher: searcher, log: log}
}

// SearchResultToVectorSearchResult converts a qdrant.SearchResult to
// the application-level VectorSearchResult type.
func SearchResultToVectorSearchResult(r SearchResult, log *zap.Logger) map[string]interface{} {
	result := map[string]interface{}{
		"score": r.Score,
	}

	if r.Payload != nil {
		if v, ok := r.Payload["asset_id"].(string); ok {
			result["asset_id"] = v
		}
		if v, ok := r.Payload["source"].(string); ok {
			result["source"] = v
		}
		if v, ok := r.Payload["name"].(string); ok {
			result["name"] = v
		}
		if v, ok := r.Payload["drive_link"].(string); ok {
			result["drive_link"] = v
		}
		if v, ok := r.Payload["media_type"].(string); ok {
			result["media_type"] = v
		}
		if v, ok := r.Payload["language"].(string); ok {
			result["language"] = v
		}
		if v, ok := r.Payload["category"].(string); ok {
			result["category"] = v
		}
		result["tags"] = r.Payload["tags"]
		result["search_text"] = r.Payload["search_text"]
	}
	return result
}
