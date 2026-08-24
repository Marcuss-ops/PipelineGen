// Package images — google_slides_adapter.go bridges the canonical
// images.ImageGenerator port implemented by the infrastructure adapter
// to the generated.ImageGeneratorPort contract that the
// generated/provider_registry.go providers consume.
//
// Why in the parent package (and not in generated):
//
//   - generated/ deliberately does NOT import images — keeping that
//     invariant means the providers remain free of any backwards
//     coupling to the parent package's evolving internal types.
//   - The adapter MUST live somewhere that knows about both surfaces;
//     images/ is the only package with that visibility.
//   - Putting the adapter here also keeps the registry call sites
//     in NewService readable: the adapter is a one-line declaration
//     aside the registry construction.
//
// Step 8 (July 2026): this file was added as part of the
// image-restructuring plan; pre-Step-8 the registry did not exist
// and the adapter was unnecessary because there was only one
// ImageGenerator wire.
package images

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/workflow/generated"
)

// ImageGeneratorAdapter is the concrete adapter that lets an
// arbitrary images.ImageGenerator backend (today: the Chrome infrastructure adapter;
// future: Flux/NVIDIA adapters) be passed to generated.NewDefaultProviderRegistry
// as the GoogleSlidesProvider delegate.
//
// Compile-time assertion: *ImageGeneratorAdapter satisfies generated.ImageGeneratorPort.
var _ generated.ImageGeneratorPort = (*ImageGeneratorAdapter)(nil)

// ImageGeneratorAdapter translates the PortGenerateRequest shape
// the generated providers use to the canonical images.GenerateImageRequest
// shape the parent-package backends expect.
type ImageGeneratorAdapter struct {
	gen ImageGenerator
}

// NewImageGeneratorAdapter wraps any ImageGenerator as a
// generated.ImageGeneratorPort. Pass nil to obtain an adapter that
// returns ErrProviderUnavailable (used in tests + stub wiring).
//
// This is the canonical constructor for the adapter; NewService
// uses it directly. No package-private alias is exposed — that
// pattern added no value over the exported constructor and would
// only obscure the call site.
func NewImageGeneratorAdapter(gen ImageGenerator) *ImageGeneratorAdapter {
	return &ImageGeneratorAdapter{gen: gen}
}

// Generate satisfies generated.ImageGeneratorPort. It maps the
// fields of PortGenerateRequest → images.GenerateImageRequest,
// invokes the wrapped backend, and maps images.GeneratedImage →
// generated.PortGeneratedImage.
func (a *ImageGeneratorAdapter) Generate(ctx context.Context, req generated.PortGenerateRequest) (*generated.PortGeneratedImage, error) {
	if a.gen == nil {
		return nil, fmt.Errorf("ImageGeneratorAdapter: no backend wired: %w", generated.ErrProviderUnavailable)
	}
	out, err := a.gen.Generate(ctx, GenerateImageRequest{
		Prompt:         req.Prompt,
		Style:          req.Style,
		Width:          req.Width,
		Height:         req.Height,
		NegativePrompt: req.NegativePrompt,
		Tags:           req.Tags,
		OutputPath:     req.OutputPath,
	})
	if err != nil {
		return nil, fmt.Errorf("ImageGeneratorAdapter: backend generate: %w", err)
	}
	return &generated.PortGeneratedImage{
		Data:       out.Data,
		Format:     out.Format,
		Width:      out.Width,
		Height:     out.Height,
		PromptUsed: out.PromptUsed,
		Provider:   out.Provider,
		SourceHash: out.SourceHash,
		OutputPath: out.OutputPath,
	}, nil
}

// TriggerPrewarm forwards the warmup signal to the underlying image backend.
// The adapter stays thin: it only relays the browser-pool hint and does not
// interpret the count.
func (a *ImageGeneratorAdapter) TriggerPrewarm(ctx context.Context, jobID string, count int) {
	if a.gen == nil {
		return
	}
	a.gen.TriggerPrewarm(ctx, jobID, count)
}
