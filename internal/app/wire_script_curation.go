// Package app — wire_script_curation.go.
//
// FASE 2.A PR2 (June 2026) split: the curation-layer adapter
// moved out of wire_script.go. The adapter bridges the concrete
// *imgservice.Service signature (which returns *asset.ImageAsset
// and takes tags []string as the search-input carrier) into the
// canonical usecase.ImageGenService typed-port shape (which
// returns *adapters.ImageResult and takes interface{} for the
// extra-input carrier).
//
// Curation scope per FASE 2.A spec: "media_curator, scene_builder,
// evidence_builder, clip_source_builder". Today the COMPLETE
// clip_source_builder implementation lives inside
// internal/application/scripts/usecase.ClipSourceBuilder (used by
// the source-cluster). MediaCurator lives in
// internal/application/scripts/dto.MediaCurator. This file owns
// the composition-root-local adapter (imageGenSvcAdapter) that
// is the seam between the concrete imgservice service and the
// usecase layer's typed ImageGenService — i.e. the "curation"
// surface where application logic meets infrastructure.
//
// Package boundary: same `package app` as wire_script.go.
// Promoting it to a sub-package would force wire_script.go to
// import a new symbol while preserving the same construction
// shape; staying in the composition root matches the
// clips_adapters_*.go + adapters_infra.go convention.
//
// Cross-references:
//   - internal/app/wire_script.go: the caller (wireScriptFlow
//     constructs & uses imageGenSvcAdapter inline in the image
//     processor registration block).
//   - internal/application/scripts/adapters: ImageGenService
//     typed-port shape (the consumer of this adapter).
//   - internal/application/images: *imgservice.Service (the
//     concrete implementation the adapter wraps).
//   - internal/domain/asset: ImageAsset (the concrete result
//     shape the adapter unwraps to SourceURL).
package app

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// imageGenSvcAdapter adapts *imgservice.Service →
// usecase.ImageGenService.
//
// The concrete SearchAndDownload takes tags []string; the typed
// interface takes extra interface{}. The bridge unwraps
// extra.([]string) (silently drops other carrier types — the
// use case only ever sends []string from the script pipeline).
//
// TODO #8 (drift-fix PR, June 2026): the previous
// `(ctx, …) (*asset.ImageAsset, error)` return shape satisfies
// the wrong interface — *adapters.ImageResult is the canonical
// typed result for ImageGenService downstream of the line-248
// NewImageProcessor call. The bridge here is the contained seam:
// any future schema drift on either side is caught at this
// single method.
type imageGenSvcAdapter struct {
	svc interface {
		SearchAndDownload(ctx context.Context, name, description, query, language string, tags []string) (*asset.ImageAsset, error)
	}
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
func (a *imageGenSvcAdapter) SearchAndDownload(ctx context.Context, name, description, query, language string, extra interface{}) (*adapters.ImageResult, error) {
	var tags []string
	if extra != nil {
		if t, ok := extra.([]string); ok {
			tags = t
		}
	}
	if a == nil || a.svc == nil {
		return nil, nil
	}
	imgAsset, err := a.svc.SearchAndDownload(ctx, name, description, query, language, tags)
	if err != nil {
		return nil, err
	}
	if imgAsset == nil {
		return &adapters.ImageResult{}, nil
	}
	return &adapters.ImageResult{SourceURL: imgAsset.SourceURL}, nil
}

// GenerateSmartImage is the second method on usecase.ImageGenService.
// It's intentionally NOT routed through the concrete *imgservice.Service
// (the concrete service doesn't expose a structurally-equivalent
// method that satisfies SearchAndDownload's typed-port contract).
// The image processor (adapter.NewImageProcessor) calls
// SearchAndDownload on every plan that requests images;
// GenerateSmartImage is reserved for future scripts-and-image
// flows and returns a typed error so the runtime preflight surfaces
// the "not supported through ImageProcessor" gap loudly instead of
// silently dropping the call.
func (a *imageGenSvcAdapter) GenerateSmartImage(ctx context.Context, name, description, style string, prompts, tags []string, width, height int, extra string, flag bool) (*asset.ImageAsset, error) {
	return nil, fmt.Errorf("GenerateSmartImage not supported through ImageProcessor")
}

// Compile-time assertion: imageGenSvcAdapter satisfies the canonical
// usecase.ImageGenService typed port. Drift here breaks the build
// immediately rather than panicking on the first script request
// that requests images.
var _ adapters.ImageGenService = (*imageGenSvcAdapter)(nil)
