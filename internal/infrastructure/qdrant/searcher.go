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
	if req.QueryVector == nil {
		return nil, fmt.Errorf("search vector must not be nil")
	}
	if req.Limit <= 0 {
		req.Limit = 20
	}

	// Validate vector dimensions against the manifest.
	if req.VectorName != "" {
		spec := s.schema.GetDense(req.VectorName)
		if spec != nil && len(req.QueryVector) != spec.Dimensions {
			return nil, &ErrVectorDimensionMismatch{
				Channel:  req.VectorName,
				Expected: spec.Dimensions,
				Actual:   len(req.QueryVector),
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

// HybridSearch performs a hybrid dense + sparse search via the Qdrant Query API
// with RRF fusion. When SparseQueryVector is nil, the method explicitly falls
// back to ANN (dense-only Search) — the caller must not label the result as
// "hybrid".
func (s *Searcher) HybridSearch(ctx context.Context, req HybridSearchRequest) ([]SearchResult, error) {
	if req.DenseVector == nil {
		return nil, fmt.Errorf("dense vector must not be nil for hybrid search")
	}
	if req.Limit <= 0 {
		req.Limit = 20
	}

	// When sparse is not available, explicitly fall back to ANN.
	// Dense-only results must never be labelled as hybrid.
	if req.SparseQueryVector == nil {
		s.log.Warn("HybridSearch: sparse vector not provided, falling back to ANN (use Search for explicit ANN)")
		return s.Search(ctx, SearchRequest{
			QueryVector: req.DenseVector,
			VectorName:  req.DenseVectorName,
			Limit:       req.Limit,
			MinScore:    req.MinScore,
			Filter:      req.Filter,
		})
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
		QueryVector: vec,
		VectorName:  vectorName,
		Limit:       limit,
		MinScore:    minScore,
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

// ── QDRANT-003: SearchAdapter moved to search_adapter.go ─────────────
// The SearchAdapter that bridges qdrant.Searcher → search.VectorStorePort
// now lives in search_adapter.go with full DTO conversion and filter building.
