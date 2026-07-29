// Package app — wire_script_curation.go.
//
// FASE 2.A PR2 (June 2026) split: the curation-layer adapter
// moved out of wire_script.go. The adapter bridges the concrete
// *imgservice.Service signature (which returns *asset.ImageAsset
// and takes tags []string as the search-input carrier) into the
// canonical adapters.ImageGenService typed-port shape (which
// returns *adapters.ImageResult). This is the only canonical
// bridge between the composition-root concrete service and the
// application-layer typed-port interface consumed by
// adapters.ImageProcessor.
//
// Curation scope per FASE 2.A spec: "media_curator, scene_builder,
// evidence_builder, clip_source_builder". Today the COMPLETE
// clip_source_builder implementation lives inside
// internal/application/scripts/usecase.ClipSourceBuilder (used by
// the source-cluster). MediaCurator lives in
// internal/application/scripts/dto.MediaCurator. This file owns
// the composition-root-local adapter (imageGenSvcAdapter) that
// is the seam between the concrete imgservice service and the
// consumer's typed ImageGenService — i.e. the "curation" surface
// where application logic meets infrastructure.
//
// DEADC-2026-07-10 / PR-DEADC-IMAGES-IMAGE-GEN-SERVICE-INTERFACE-CONTRACT
// (Phase F P2): the previous always-error GenerateSmartImage
// method was REMOVED from this adapter. The shim no longer
// pretends to implement usecase.ImageGenService (which it never
// structurally did — the 10-arg usecase GenerateSmartImage had
// a different signature than the consumer's smartImageGenService
// type-assertion target). The shim now ONLY implements the
// adapters.ImageGenService interface (single method:
// SearchAndDownload) + the optional imagePrewarmer interface
// (TriggerPrewarm). The composition root injects the canonical
// *images.Service directly; the shim is a pure, non-broken
// type-bridge.
//
// Package boundary: same `package app` as wire_script.go.
// Promoting it to a sub-package would force wire_script.go to
// import a new symbol while preserving the same construction
// shape; staying in the composition root matches the
// clips_adapters_*.go + adapters_infra.go convention.
//
// Cross-references:
//   - internal/app/wire_script_postprocess.go: the caller
//     (registerScriptPostProcessors constructs & uses
//     imageGenSvcAdapter inline in the image processor
//     registration block at line ~109).
//   - internal/application/scripts/adapters/processor_images.go:
//     the consumer (adapters.ImageGenService + imagePrewarmer
//     typed-port shapes).
//   - internal/application/images: *imgservice.Service (the
//     concrete implementation the adapter wraps).
//   - internal/domain/asset: ImageAsset (the concrete result
//     shape the adapter unwraps to SourceURL).
package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// imageGenSvcAdapter adapts *imgservice.Service →
// adapters.ImageGenService (the canonical typed port consumed by
// adapters.ImageProcessor).
//
// The adapter invokes AI image generation (GenerateSmartImage) via
// the wrapped concrete service and bridges the result shape
// (*asset.ImageAsset) into the consumer's typed-port shape
// (*adapters.ImageResult). On failure the error is propagated.
// The concrete SearchAndDownload path of *imgservice.Service (a
// Wikipedia/SearXNG/DuckDuckGo web-search fallback) is intentionally
// NOT used: the caller wants AI-generated images.
//
// DEADC-2026-07-10 / PR-DEADC-IMAGES-IMAGE-GEN-SERVICE-INTERFACE-CONTRACT
// (Phase F P2): the previous always-error GenerateSmartImage
// method was REMOVED — the shim no longer claims to implement
// usecase.ImageGenService (which it never structurally did, since
// the 10-arg usecase GenerateSmartImage had a different signature
// than the consumer's smartImageGenService type-assertion target).
// The shim now ONLY implements adapters.ImageGenService (single
// method: SearchAndDownload) + the optional imagePrewarmer
// interface (TriggerPrewarm). The composition root injects the
// canonical *images.Service directly; the shim is a pure,
// non-broken type-bridge.
type imageGenSvcAdapter struct {
	svc interface {
		GenerateSmartImage(ctx context.Context, subject, topic, style string, prompts, tags []string, width, height int, model string, skipDrive bool) (*asset.ImageAsset, error)
		SearchAndDownload(ctx context.Context, name, description, query, language string, tags []string) (*asset.ImageAsset, error)
		TriggerPrewarm(ctx context.Context, jobID string, count int)
	}
}

// GenerateSmartImage preserves the optional smart-image capability across
// the composition boundary. Without this forwarding method the adapter was
// seen only as the generic SearchAndDownload port, so scene generation
// bypassed the visual-prompt path in the ImageProcessor.
func (a *imageGenSvcAdapter) GenerateSmartImage(ctx context.Context, subject, topic, style string, prompts, tags []string, width, height int, model string, skipDrive bool) (*asset.ImageAsset, error) {
	if a == nil || a.svc == nil {
		return nil, fmt.Errorf("image generation service is not configured")
	}
	return a.svc.GenerateSmartImage(ctx, subject, topic, style, prompts, tags, width, height, model, skipDrive)
}

// SearchAndDownload bridges the concrete *imgservice.Service
// signature (returns *asset.ImageAsset) to the canonical
// adapters.ImageGenService interface (returns
// *adapters.ImageResult). ImageResult exposes only SourceURL, so
// the bridge copies that single field after a defensive nil-check
// on the underlying asset.ImageAsset. A nil inner result becomes
// an EMPTY ImageResult (SourceURL="") so the downstream
// ImageProcessor gets a typed non-nil pointer — matching the
// existing processor code path in
// internal/application/scripts/adapters/processor_images.go::Process.
//
// AI-first: GenerateSmartImage is called with the scene query as
// prompt. On failure the error is propagated — no web search fallback.
func (a *imageGenSvcAdapter) SearchAndDownload(ctx context.Context, name, description, query, language string) (*adapters.ImageResult, error) {
	if a == nil || a.svc == nil {
		return nil, nil
	}

	imgAsset, err := a.svc.GenerateSmartImage(ctx, name, query, "", []string{query}, nil, 1920, 1080, "", false)
	if err != nil {
		return nil, fmt.Errorf("AI image generation failed for %q: %w", name, err)
	}
	if imgAsset == nil {
		return &adapters.ImageResult{}, nil
	}

	url := imgAsset.SourceURL
	if !strings.HasPrefix(url, "http") && imgAsset.DriveFileID != "" {
		url = fmt.Sprintf("https://drive.google.com/file/d/%s/view", imgAsset.DriveFileID)
	}
	return &adapters.ImageResult{SourceURL: url, DriveFileID: imgAsset.DriveFileID}, nil
}

// TriggerPrewarm forwards the warmup signal to the concrete image
// service so the processor's optional warmup seam can activate the
// browser/session pool before the parallel fan-out begins.
func (a *imageGenSvcAdapter) TriggerPrewarm(ctx context.Context, jobID string, count int) {
	if a == nil || a.svc == nil {
		return
	}
	a.svc.TriggerPrewarm(ctx, jobID, count)
}

// Compile-time assertions: imageGenSvcAdapter satisfies the canonical
// adapters.ImageGenService typed port (single method: SearchAndDownload)
// AND the optional imagePrewarmer interface (TriggerPrewarm). Drift in
// either interface breaks the build immediately rather than panicking
// on the first script request that requests images.
var (
	_ adapters.ImageGenService = (*imageGenSvcAdapter)(nil)
	_ imagePrewarmer           = (*imageGenSvcAdapter)(nil)
)

// imagePrewarmer mirrors the typed-port interface declared in
// internal/application/scripts/adapters/processor_images.go. The
// shim satisfies this interface so the consumer's
// prewarmer, ok := p.gen.(imagePrewarmer) type-assertion idiom works
// without requiring app to import the adapters package (which
// would create a circular import: app → adapters → app).
type imagePrewarmer interface {
	TriggerPrewarm(ctx context.Context, jobID string, count int)
}
