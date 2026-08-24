// Package generated (application/images/generated) — provider.go
// declares the GenerationProvider interface and the minimal backend
// contract (ImageGeneratorPort). Per PR-IMG-SPLIT-5 (July 2026),
// interfaces live in their own file, separate from concrete providers
// and the registry.
//
// Google Slides, driven through Chrome/Playwright and Nano Banana Pro,
// is the only supported generation path. Flux and NVIDIA provider
// stubs were removed deliberately: unavailable providers must not
// appear in registries, diagnostics, model routing, or public
// capability surfaces.
package images

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// GenerationProvider is the single backend contract for AI image generation.
// Production composition must register GoogleSlidesProvider and nothing else.
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
