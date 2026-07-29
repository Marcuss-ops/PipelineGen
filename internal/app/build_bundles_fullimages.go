// Package app — build_bundles_fullimages.go: composition-root surface for
// the FullImages module.
//
// PR-IMAGES-FULLIMAGES-IMAGE-ONLY (2026-07-10, IMAGES-LEGACY-CLEANUP
// wave, CUTOVER phase, deadline 2026-09-01): the pre-CUTOVER 4-gate
// composition contract (ImageService / FfmpegProc / Publisher /
// ImagesDir) is REDUCED to 2 mandatory gates (ImageService /
// ImagesDir). The retired FfmpegProc + Publisher deps were ONLY
// consumed by the now-retired Ken Burns video pipeline inside
// mediafullimages.NewService (processGeneratedVideo /
// uploadAndFinish / publishToDrive). The image-only path consumes
// ONLY `s.imgService.GenerateSmartImage` (which performs the Drive
// upload internally via the skipDrive=false contract).
//
// Background: the canonical fullimages service lives in
// `internal/application/images/fullimages/` and the canonical HTTP
// handler lives in `internal/api/fullimages/handler.go`, mounted on
// the /api/fullimages/* prefix (matches the gen_api_docs.go entry
// that has been the canonical URL since PR3 Wave 14, June 2026,
// updated post-PR-IMAGES-FULLIMAGES-IMAGE-ONLY to /image/generate).
//
// godlike/06 SSOT (one canonical owner per fact): the
// `internal/api/fullimages/` package is the SOLE owner of the
// public fullimages wire-shape (FullImagesRequest / FullImagesResponse
// / FullImagesHandler). The composition root is the SOLE owner of
// the wire-up of the application-layer service to the HTTP handler.
// No other package re-exports these types.
//
// godlike/07 no-fake-availability: the 2 mandatory gates are checked
// UPFRONT in WireFullImages (ImageService / ImagesDir are the 2
// remaining wiring deps; nil on either yields a typed error which
// the caller — registerInternalModules Step 7 — downgrades to
// log.Warn + skip-route + return-nil). Composition boot MUST NOT
// abort because FullImages is optional in the architecture (matches
// the godlike/07 fail-closed-at-composition contract applied to
// Artlist + StockPipeline + YouTubeClip).
//
// Single-function shape (WireFullImages) mirrors the existing
// WireArtlist precedent in build_bundles_artlist.go (Blocco C1-Step 3
// scope) and the WireMediaIngest / WireYouTubeClip / WireStockPipeline
// precedent in the same package.
package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	fullimagesapi "github.com/Marcuss-ops/PipelineGen/internal/api/fullimages"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	mediafullimages "github.com/Marcuss-ops/PipelineGen/internal/application/images/fullimages"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

// FullImagesBundle is the capability bundle for the FullImages module.
//
// PR-IMAGES-FULLIMAGES-IMAGE-ONLY (2026-07-10, CUTOVER phase): the
// pre-CUTOVER 4-field bundle (ImageService / FfmpegProc / Publisher /
// ImagesDir) is REDUCED to 2 fields (ImageService / ImagesDir) — the
// FfmpegProc + Publisher fields were ONLY consumed by the now-retired
// Ken Burns video pipeline inside mediafullimages.NewService.
//
// The composition root owns the resolution of root.Domains.ImageService
// + cfg.Storage.ImagesPath() — these are the canonical cross-bundle
// reads that WireFullImages threads through the bundle.
type FullImagesBundle struct {
	ImageService *imgservice.Service
	ImagesDir    string
}

// WireFullImages constructs the FullImages module wiring (handler +
// module) from the canonical FullImagesBundle. Returns a typed
// *wiring.FullImagesWiring on success; nil + typed error on any of the 2
// mandatory gates (godlike/07 fail-closed-at-composition).
//
// PR-IMAGES-FULLIMAGES-IMAGE-ONLY (2026-07-10, CUTOVER phase): the
// pre-CUTOVER 4-gate check (ImageService / FfmpegProc / Publisher /
// ImagesDir) is REDUCED to 2-gate check (ImageService / ImagesDir)
// — the retired FfmpegProc + Publisher gates were ONLY consumed by
// the now-retired Ken Burns video pipeline.
//
// enabledFunc: the route module is gated on `cfg.Features.ImagesEnabled`
// (the operator feature flag for the images capability family, of
// which fullimages is a subset) — matches the artlist pattern at
// WireArtlist's api.NewRouteModule call site. This lets operators
// disable the fullimages route at boot without code changes.
func WireFullImages(bundle *FullImagesBundle, cfg *config.Config, log *zap.Logger) (*wiring.FullImagesWiring, error) {
	if cfg == nil {
		return nil, fmt.Errorf("WireFullImages: cfg is nil")
	}
	// godlike/07 fail-closed: 2 mandatory gates UPFRONT. nil on
	// either yields a typed error which the caller
	// (registerInternalModules Step 7) downgrades to log.Warn +
	// skip-route + return-nil. The check order matches the
	// constructor-arg order of mediafullimages.NewService
	// (imgService, imagesDir, log) so the diagnostic message maps
	// 1:1 to the missing arg.
	if bundle == nil {
		return nil, fmt.Errorf("WireFullImages: bundle is nil")
	}
	if bundle.ImageService == nil {
		return nil, fmt.Errorf("WireFullImages: bundle.ImageService is nil (GenerateSmartImage dep)")
	}
	if bundle.ImagesDir == "" {
		return nil, fmt.Errorf("WireFullImages: bundle.ImagesDir is empty (Storage.ImagesPath() unresolved)")
	}

	// godlike/06 SSOT: the application-layer service is the canonical
	// SOLE owner of the section → image generation logic
	// (GenerateForSections). Composition root is the SOLE owner of
	// the DI wiring (the 2 deps → service ctor args).
	svc := mediafullimages.NewService(
		bundle.ImageService,
		bundle.ImagesDir,
		log,
	)

	// godlike/06 SSOT: the public HTTP handler is the canonical SOLE
	// owner of the wire-shape (FullImagesRequest / FullImagesResponse
	// / FullImagesHandler) — all 3 live in
	// internal/api/fullimages/handler.go.
	handler := fullimagesapi.NewFullImagesHandler(svc)

	// godlike/06 SSOT: api.NewRouteModule is the canonical SOLE
	// owner of the route-publication contract. The prefix is
	// /api/fullimages (NOT /api/images) so the resulting public
	// URL is /api/fullimages/image/generate (matches the canonical
	// gen_api_docs.go entry post-PR-IMAGES-FULLIMAGES-IMAGE-ONLY
	// CUTOVER phase). The enabledFunc gates the route on
	// cfg.Features.ImagesEnabled (ImagesEnabled is the canonical
	// feature flag for the images capability family, of which
	// fullimages is a subset). Mirrors WireArtlist's pattern at
	// the api.NewRouteModule call site in build_bundles_artlist.go.
	mod := api.NewRouteModule(
		"fullimages",
		func() bool { return cfg.Features.ImagesEnabled },
		"/fullimages",
		handler,
		log,
	)

	log.Info("WireFullImages: fullimages module wired",
		zap.String("route_prefix", "/fullimages"),
		zap.String("public_url", "/api/fullimages/image/generate"),
		zap.Bool("godlike_07_fail_closed", true),
	)

	return &wiring.FullImagesWiring{
		Module: mod,
	}, nil
}
