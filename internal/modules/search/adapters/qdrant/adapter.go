// Package qdrant provides the Qdrant-backed adapter that will satisfy
// ports.Searcher (SearchForScenes) and ports.Embedder (EmbedText).
//
// Status: STUB. The legacy implementation under
// internal/media/vectorstore must be migrated into this package before
// the search module is wired live. Until then the constructor returns a
// typed Adapter that's safe to inject as a port dependency — runtime
// calls fail with ErrNotImplemented so misconfigured wiring is loud.
package qdrant

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/modules/search/domain"
	"github.com/Marcuss-ops/PipelineGen/internal/modules/search/ports"
)

// ErrNotImplemented is returned by the stub. Use it in tests to assert
// that the migration has not yet happened.
var ErrNotImplemented = errors.New("qdrant: adapter not yet implemented (migration PR pending)")

// Adapter is the Qdrant adapter struct. Future fields will own a
// qdrant.Client plus per-collection vector config (text / transcript /
// visual / bm25 — see AGENTS.md §"Vector Spaces").
type Adapter struct {
	Collection string
	BaseURL    string
}

// New returns a stub Adapter. Wire it into the search module's
// ports.Searcher once the real Qdrant client migration lands.
func New(collection, baseURL string) *Adapter {
	return &Adapter{Collection: collection, BaseURL: baseURL}
}

// SearchForScenes satisfies ports.Searcher. Stub: returns ErrNotImplemented.
// Future implementation should query /collections/{name}/points/search
// using the dense ANN + hybrid RRF path documented in AGENTS.md.
func (a *Adapter) SearchForScenes(_ context.Context, queries []domain.SceneQuery) ([]domain.SceneCandidates, error) {
	return nil, ErrNotImplemented
}

// Compile-time check: the stub matches the Searcher port. Agent 1 can
// plug *Adapter directly into Dependencies.Searcher.
var _ ports.Searcher = (*Adapter)(nil)
