// Package embedding defines the canonical contract for text-embedding
// generators consumed by the application layer. Concrete implementations
// live in internal/infrastructure/embeddings/ (PR-D.5.1 split:
//
//   - application/<X>/ holds business logic and depends on Embedder
//   - infrastructure/embeddings/ holds concrete PythonScriptEmbedder
//     (subprocess) and HTTPEmbedder (sidecar client) implementations.
//
// This separation enforces AGENTS.md architectural split: the
// application layer must NOT directly call os/exec or talk to a
// sidecar HTTP server — it depends on this interface and receives the
// concrete implementation at construction time from internal/app/
// composition root.
package asset

import "context"

// Embedder generates semantic embedding vectors for text. Both inputs
// and outputs ([]float32) match what the Python e5-base-multilingual
// script in bridges/generate_embedding.py returns; the sidecar HTTP
// adapter (infrastructures/embeddings/http.go) returns []float64 but
// the application layer normalises to []float32 via the wrapper.
type Embedder interface {
	// Embed returns a vector representation of text. Empty text is
	// permitted and returns (nil, nil) so callers can short-circuit on
	// blank-input pipelines without an error.
	Embed(ctx context.Context, text string) ([]float32, error)
}
