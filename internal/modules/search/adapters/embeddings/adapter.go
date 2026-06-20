// Package embeddings provides the multilingual-e5-base-backed adapter
// that will satisfy ports.Embedder (EmbedText).
//
// Status: STUB. The legacy embedding client must be migrated into this
// package before the search module is wired live. The stub returns
// ErrNotImplemented so misconfigured wiring is loud.
package embeddings

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/modules/search/ports"
)

// ErrNotImplemented is returned by the stub.
var ErrNotImplemented = errors.New("embeddings: adapter not yet implemented (migration PR pending)")

// Adapter placeholder struct. Future will own the multilingual-e5-base
// client producing 768-dim "text" vectors per AGENTS.md §"Vector Spaces".
type Adapter struct {
	ModelName string
	Dim       int
}

// New returns a stub Adapter with a default 768-dim multilingual-e5-base
// profile; override Dim at call sites that need a different model.
func New(modelName string) *Adapter {
	return &Adapter{ModelName: modelName, Dim: 768}
}

// NewWithDim lets callers wire a non-default dim (e.g. 512 for CLIP).
func NewWithDim(modelName string, dim int) *Adapter {
	return &Adapter{ModelName: modelName, Dim: dim}
}

// EmbedText satisfies ports.Embedder. Stub: returns ErrNotImplemented.
func (a *Adapter) EmbedText(_ context.Context, _ string) ([]float32, error) {
	return nil, ErrNotImplemented
}

// Compile-time check.
var _ ports.Embedder = (*Adapter)(nil)
