// Package reranker provides the CrossEncoder-backed adapter that will
// satisfy ports.Reranker (Rerank).
//
// Status: STUB. The legacy implementation (BGE-reranker-v2-m3) must be
// migrated into this package before the search module is wired live.
// The stub returns ErrNotImplemented so misconfigured wiring is loud.
package reranker

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/modules/search/domain"
	"github.com/Marcuss-ops/PipelineGen/internal/modules/search/ports"
)

// ErrNotImplemented is returned by the stub.
var ErrNotImplemented = errors.New("reranker: adapter not yet implemented (migration PR pending)")

// Adapter placeholder struct. Future field will own a CrossEncoder
// client (BGE-reranker-v2-m3 today, see AGENTS.md §"Qdrant Entity
// Associations" → "Reranker optional").
type Adapter struct {
	ModelName string
}

// New returns a stub Adapter.
func New(modelName string) *Adapter {
	return &Adapter{ModelName: modelName}
}

// Rerank satisfies ports.Reranker. Stub: returns ErrNotImplemented.
func (a *Adapter) Rerank(_ context.Context, _ string, candidates []domain.SceneCandidate, _ int) ([]domain.SceneCandidate, error) {
	return nil, ErrNotImplemented
}

// Compile-time check.
var _ ports.Reranker = (*Adapter)(nil)
