package script

import "context"

// MetadataGenerator is the technology-independent video metadata port. The
// concrete Ollama adapter lives in internal/platform/ollama/adapters and
// implements this interface without importing the scripts business package.
type MetadataGenerator interface {
	GenerateMetadata(ctx context.Context, req MetadataGenerationRequest) ([]VideoMetadata, error)
}
