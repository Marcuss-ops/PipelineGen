// Package generated (application/images/generated) — provider.go
// declares the GenerationProvider interface and the minimal backend
// contract (ImageGeneratorPort). Per PR-IMG-SPLIT-5 (July 2026),
// interfaces live in their own file, separate from concrete providers
// and the registry.
//
// The generated package has no active generation provider as of the
// Chrome/Playwright + Google Slides retirement; the interfaces here
// remain as the typed contract for any future backend that wires into
// the registry pipeline.
package generated

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// GenerationProvider is the backend contract for AI image generation.
// Production composition must register an explicit provider; the
// generated package itself ships fail-closed with no default backend.
type GenerationProvider interface {
	Generate(ctx context.Context, req GenerateRequest, opts GenerateOptions) (*GeneratedImage, error)
	TriggerPrewarm(ctx context.Context, jobID string, count int)
	Name() asset.ImageProvider
	Healthy(ctx context.Context) error
}

// ImageGeneratorPort is the minimal contract the registry needs to invoke the
// Chrome/Playwright backend without importing the parent images package.
type ImageGeneratorPort interface {
	Generate(ctx context.Context, req PortGenerateRequest) (*PortGeneratedImage, error)
	TriggerPrewarm(ctx context.Context, jobID string, count int)
}
