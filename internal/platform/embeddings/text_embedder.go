package embeddings

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// TextEmbedder is the narrow text-embedding contract used by catalog
// query construction and readiness probes.
type TextEmbedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

type textEmbedderAdapter struct {
	inner asset.Embedder
}

// NewTextEmbedderAdapter adapts the canonical domain-level asset.Embedder
// contract to the narrow TextEmbedder shape.
func NewTextEmbedderAdapter(inner asset.Embedder) TextEmbedder {
	return &textEmbedderAdapter{inner: inner}
}

var _ TextEmbedder = (*textEmbedderAdapter)(nil)

func (a *textEmbedderAdapter) Embed(ctx context.Context, text string) ([]float32, error) {
	result, err := a.inner.Embed(ctx, text)
	if err != nil {
		return nil, err
	}
	return result.Vector, nil
}
