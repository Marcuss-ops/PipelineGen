package ports

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/modules/search/domain"
)

// Searcher is the canonical port other modules (e.g. scriptgen) consume
// to obtain semantic matches for scenes. Adapters live in
// internal/modules/search/adapters/*.
type Searcher interface {
	SearchForScenes(ctx context.Context, queries []domain.SceneQuery) ([]domain.SceneCandidates, error)
}

// Embedder produces dense vectors from free text. Used internally by the
// Qdrant adapter to populate the "text" / "transcript" / "visual"
// named vectors per AGENTS.md §"Vector Spaces".
type Embedder interface {
	EmbedText(ctx context.Context, text string) ([]float32, error)
}

// Reranker cross-encodes (query, candidate) pairs to refine Top-K.
// Optional: when nil, Search should simply skip the rerank phase.
type Reranker interface {
	Rerank(ctx context.Context, query string, candidates []domain.SceneCandidate, topK int) ([]domain.SceneCandidate, error)
}
