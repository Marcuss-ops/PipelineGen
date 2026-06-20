package ports

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/modules/search/domain"
)

// AssetSearch is the scriptgen module's view of the search subsystem.
// It is the contract used by Sync and Async paths of the unified
// GenerateScript use case to resolve scenes into clip candidates.
//
// Sync: the contract defined for Agent 2 — scriptgen depends ONLY on
// this interface; Qdrant, BM25, the reranker and embeddings are hidden
// behind it.
type AssetSearch interface {
	SearchForScenes(ctx context.Context, queries []domain.SceneQuery) ([]domain.SceneCandidates, error)
}
