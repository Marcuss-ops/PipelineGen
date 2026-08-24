// Package images — provider_dispatch.go: GenerationProviderRegistry
// dispatch ONLY (PR-GODOBJ-3-IMAGES-GENERATION, July 2026).
//
// godlike/06 SSOT: one canonical owner per fact — the dispatch concern
// for image generation lives ONLY here. ALL callers (sync adapter +
// async job adapter) route through dispatchToRegistry.
//
// PR-GODOBJ-3 KILL LIST (a) applied: fallback to legacy `imageGen.Generate`
// is REMOVED entirely. A nil registry returns typed
// ErrNoGenerationProviderWired (godlike/07 typed-error contract) —
// composition that does NOT wire a registry will produce a fail-closed
// error rather than silently fall through to a stale direct port.
package images

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/workflow/generated"
)

// ErrNoGenerationProviderWired is the typed sentinel returned when
// GenerationProviderRegistry is nil (PR-GODOBJ-3 KILL LIST a: legacy
// imageGen.Generate fallback is REMOVED; composition must wire
// NewGenerationProviderRegistry or the dispatch fails-closed).
// godlike/07 typed-error contract: errors.Is(err, ErrNoGenerationProviderWired).
var ErrNoGenerationProviderWired = errors.New(
	"images: GenerationProviderRegistry not wired (PR-GODOBJ-3 KILL LIST a — legacy imageGen.Generate fallback REMOVED; composition must wire NewGenerationProviderRegistry from internal/capabilities/images/workflow/generated/provider_registry.go)",
)

// dispatchToRegistry routes GenerateImageRequest through the registry.
// A nil registry returns ErrNoGenerationProviderWired (KILL LIST a
// applied — NO silent fall-through to imageGen.Generate).
func dispatchToRegistry(
	ctx context.Context,
	registry *generated.GenerationProviderRegistry,
	req GenerateImageRequest,
) (*GeneratedImage, error) {
	if registry == nil {
		return nil, ErrNoGenerationProviderWired
	}
	out, err := registry.Generate(ctx, generated.GenerateRequest{
		Prompt:         req.Prompt,
		Style:          req.Style,
		Width:          req.Width,
		Height:         req.Height,
		Tags:           req.Tags,
		NegativePrompt: req.NegativePrompt,
		OutputPath:     req.OutputPath,
	}, generated.GenerateOptions{})
	if err != nil {
		return nil, err
	}
	return &GeneratedImage{
		Data:       out.Data,
		Format:     out.Format,
		Width:      out.Width,
		Height:     out.Height,
		PromptUsed: out.PromptUsed,
		Provider:   string(out.Provider),
		SourceHash: out.SourceHash,
		OutputPath: out.OutputPath,
	}, nil
}
