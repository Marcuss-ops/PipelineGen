// Package application hosts the use cases for the search bounded
// context. It coordinates search ports and adapters; the package is the
// only place shell orchestration logic lives for the search module.
package application

import (
	"errors"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/modules/search/ports"
)

// Dependencies is the runtime-injected graph for the search module.
// Searcher and Embedder are required; Reranker is optional.
type Dependencies struct {
	Searcher ports.Searcher
	Embedder ports.Embedder
	Reranker ports.Reranker // optional
	Log      *zap.Logger    // optional
}

// Module is the application-level facade for the search module. It owns
// composition; Agent 1 wraps *Module into an api.Module.
type Module struct {
	deps Dependencies
}

// NewModule is the entry point for composition. Following the contract
// shared with the scriptgen module: a missing required dependency fails
// the constructor, never the runtime.
func NewModule(deps Dependencies) (*Module, error) {
	if deps.Searcher == nil {
		return nil, errors.New("search: Searcher dependency is required")
	}
	if deps.Embedder == nil {
		return nil, errors.New("search: Embedder dependency is required")
	}
	return &Module{deps: deps}, nil
}

// MustNewModule panics when dependencies are missing. Intended for tests.
func MustNewModule(deps Dependencies) *Module {
	m, err := NewModule(deps)
	if err != nil {
		panic(err)
	}
	return m
}

// Searcher returns the configured Searcher. Composition roots (Agent 1)
// pass this through to the scriptgen module's AssetSearch port.
func (m *Module) Searcher() ports.Searcher {
	return m.deps.Searcher
}

// Embedder returns the configured Embedder.
func (m *Module) Embedder() ports.Embedder {
	return m.deps.Embedder
}

// HasReranker reports whether a Reranker adapter was injected.
func (m *Module) HasReranker() bool {
	return m.deps.Reranker != nil
}
