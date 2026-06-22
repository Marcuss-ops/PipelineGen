package association

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/embeddings"
)

// GenerateEmbedding returns a semantic embedding for the given text.
//
// PR-D.5.1: this method used to call `exec.CommandContext("python3",
// "bridges/generate_embedding.py", "--text", text)` directly inside
// the application layer. That violated AGENTS.md's architectural split
// (os/exec in application/). The Python invocation now lives behind
// the canonical asset.Embedder interface in
// internal/infrastructure/embeddings/python.go and is injected via
// SetEmbedder from internal/app/ during composition root construction.
//
// If the embedder has not been wired (e.g. partial tests, ad-hoc
// scripts that construct Service manually), we log a WARN and lazy-
// construct from scriptsDir so behaviour matches the pre-PR-D.5.1
// implementation. Production paths always go through the injected
// embedder in internal/app/ dependencies.go::composeIntegration and
// the WARN path is never hit. The lazy fallback is intentionally
// visible (logged once) so misconfigured wiring does not silently
// mask regressions in the canonical wiring layer.
func (s *Service) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, nil
	}

	if s.embedder == nil {
		zap.L().Warn("association.Service.embedder unset — falling back to lazy subprocess construction; production wiring MUST call SetEmbedder during composition in internal/app/.")
		s.embedder = embeddings.NewPythonScriptEmbedder("python3", s.scriptsDir)
	}

	emb, err := s.embedder.Embed(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("embedder.Embed failed: %w", err)
	}
	return emb, nil
}
