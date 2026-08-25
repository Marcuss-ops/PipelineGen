// Package images — generation_usecase.go: pure orchestration of the
// canonical image-generation pipeline (PR-GODOBJ-3-IMAGES-GENERATION,
// July 2026).
//
// godlike/06 SSOT: canonical owner of the request → image pipeline.
// Strictly persistence-agnostic — RunUsage never writes to the DB.
//   - sync_generation.go consumes the usecase output and calls
//     storage.IngestImage (sync path is allowed to ingest directly).
//   - generation_job.go consumes the usecase output and emits the typed
//     *job.ArtifactManifest sidecar to the runner finalizer; the
//     finalizer handles media_assets persistence (per KILL LIST b).
//
// godlike/07 honest-limitation disclosure (AGENTS.md Check 44 LoC cap):
// This file exceeds the 66-LoC transitional cap (~120 LoC) because
// prompt composition + style resolution + dimension merging +
// registry dispatch + manifest wiring are inherent to the
// deterministic 5-step pipeline. Forward-pointer linked_issue:
// PR-GODOBJ-3b-USECASE-SLIM extracts promptComposer → pkg/promptcompose
// + per-capability helper files (≤30 LoC each).
//
// KILL LIST applied (PR-GODOBJ-3):
//
//	(a) NO legacy imageGen.Generate fallback — ErrNoGenerationProviderWired
//	    when registry is nil (godlike/07 typed-error contract).
//	(b) NO inline IngestImage call — sync adapter + async finalizer own
//	    persistence; usecase produces ONLY (output, manifest).
//	(c) NO account/project params — GenerateSmartImageWithAccount is
//	    REMOVED; this file's UsecaseCommand lacks those fields. Tenant
//	    identity lives in a separate auth/tenancy port (not in image
//	    generation).
package images

import (
	"context"
	"fmt"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
	"strings"
)

// UsecaseDeps bundles the dependencies the usecase needs.
// Composition wires these from the canonical ports (PR-GODOBJ-3 KILL
// LIST a: NO imageGen field per the kill-list — only registry).
type UsecaseDeps struct {
	Registry *GenerationProviderRegistry
	Styles   StyleResolver
	Log      *zap.Logger
}

// UsecaseCommand is the canonical typed input to RunUsage.
// PR-GODOBJ-3 KILL LIST c: NO account/project fields.
type UsecaseCommand struct {
	JobID      string
	Position   int
	Prompt     string
	Style      string
	Model      string
	Width      int
	Height     int
	Tags       []string
	OutputPath string
}

// UsecaseOutput is the persistence-agnostic envelope returned by the usecase:
//   - OutputPath: on-disk path where the provider wrote the image.
//   - Manifest:   typed *job.ArtifactManifest sidecar (sync adapter drops
//     this; async job emits it via job.ManifestKey in handlerResult).
//   - Result:     *GeneratedImage with Data/Format/PromptUsed metadata.
type UsecaseOutput struct {
	OutputPath string
	Manifest   *job.ArtifactManifest
	Result     *GeneratedImage
}

// RunUsage is the deterministic 5-step image-generation pipeline.
//
//	(1) Resolve style fail-closed via StyleResolver (nil-tolerant).
//	(2) Compose prompt via promptComposer (request-side + style suffix).
//	(3) Validate dimensions (caller < canonical default).
//	(4) Dispatch through registry ONLY (KILL LIST a — NO imageGen fallback).
//	(5) Build typed ArtifactManifest for the Sender-side upload cycle.
//
// NO DB writes. The usecase is persistence-agnostic per KILL LIST b.
//
// Step-1 typed migration (PR-IMAGES-AI-VS-NORMAL-PLAN, A1, July 2026):
// step (3) lost the `resolved.W/H fallback` tier — StyleDefinition
// no longer carries DefaultWidth/DefaultHeight, so dimensions are
// caller-supplied OR default to 1920x1080 (PipelineGen's canonical
// 16:9 image-generation aspect).
func RunUsage(ctx context.Context, deps UsecaseDeps, cmd UsecaseCommand) (*UsecaseOutput, error) {
	if deps.Registry == nil {
		return nil, ErrNoGenerationProviderWired
	}

	resolved, err := resolveStyle(deps.Styles, cmd.Style, cmd.Model)
	if err != nil {
		return nil, fmt.Errorf("style resolution: %w", err)
	}

	finalPrompt := promptComposer(cmd.Prompt, resolved.PromptSuffix)

	// Step-1 typed migration (A1, July 2026): removed
	// `finalWidth = resolved.Width` / `finalHeight = resolved.Height`
	// fallback tiers. Dimensions are caller-supplied OR canonical,
	// NO per-style overrides.
	finalWidth := cmd.Width
	if finalWidth == 0 {
		finalWidth = 1920
	}
	finalHeight := cmd.Height
	if finalHeight == 0 {
		finalHeight = 1080
	}

	result, err := dispatchToRegistry(ctx, deps.Registry, GenerateImageRequest{
		Prompt:         finalPrompt,
		NegativePrompt: resolved.NegativePrompt,
		Style:          cmd.Style,
		Width:          finalWidth,
		Height:         finalHeight,
		Tags:           cmd.Tags,
		OutputPath:     cmd.OutputPath,
	})
	if err != nil {
		return nil, fmt.Errorf("image generation failed: %w", err)
	}

	// Use the actual output path the provider wrote to (NOT the
	// requested cmd.OutputPath which may be empty when the provider
	// chooses its own path).
	manifestPath := result.OutputPath
	if manifestPath == "" {
		manifestPath = cmd.OutputPath
	}
	manifest, err := buildImageManifest(cmd.JobID, cmd.Position, manifestPath, result.Format)
	if err != nil {
		return nil, fmt.Errorf("artifact manifest build: %w", err)
	}
	if vErr := manifest.Validate(); vErr != nil {
		return nil, fmt.Errorf("artifact manifest validate: %w", vErr)
	}

	if deps.Log != nil {
		deps.Log.Info("image generation usecase completed",
			zap.String("job_id", cmd.JobID),
			zap.String("output_path", manifestPath),
			zap.String("provider", string(result.Provider)),
			zap.Int("width", result.Width),
			zap.Int("height", result.Height),
		)
	}

	return &UsecaseOutput{
		OutputPath: manifestPath,
		Manifest:   manifest,
		Result:     result,
	}, nil
}

// resolveStyle is the internal helper that calls StyleResolver.Resolve
// with safe defaults (nil = no style resolution).
func resolveStyle(resolver StyleResolver, style, model string) (ResolvedStyle, error) {
	if resolver == nil {
		return ResolvedStyle{}, nil
	}
	return resolver.Resolve(style, "", model)
}

// promptComposer combines the user prompt + the resolved style suffix
// into the final prompt.
//   - Empty style suffix → prompt unchanged.
//   - Empty prompt + non-empty suffix → suffix becomes the prompt.
//   - Otherwise → prompt + ", " + suffix.
func promptComposer(originalPrompt, styleSuffix string) string {
	original := strings.TrimSpace(originalPrompt)
	suffix := strings.TrimSpace(styleSuffix)
	if suffix == "" {
		return original
	}
	if original == "" {
		return suffix
	}
	return original + ", " + suffix
}
