package youtube

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/youtube/types"
)

// generateClipMetadata generates rich metadata for a clip using Ollama.
// Delegates to the metadata capability service (PR5 Phase 1).
func (s *Service) generateClipMetadata(ctx context.Context, title, transcript, description string) *types.ClipRichMetadata {
	if s.metadata == nil {
		return nil
	}
	return s.metadata.GenerateClipMetadata(ctx, title, transcript, description)
}

// metadataMetadataModel returns the model to use for metadata generation.
func (s *Service) metadataMetadataModel() string {
	if s == nil || s.cfg == nil {
		return "gemma4:e2b"
	}
	return s.cfg.External.OllamaMetadataModel
}
