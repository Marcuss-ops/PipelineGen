// Package app — build_bundles_fullimages.go: composition-root surface for
// the FullImages module (PR-IMG-LEGACY-6, IMAGES-LEGACY-CLEANUP-2026-07-06
// wave, CUTOVER phase, 2026-07-06, deadline 2026-08-22).
//
// Background: the canonical fullimages service lives in
// `internal/application/images/fullimages/` and the canonical HTTP
// handler lived (until PR-IMG-LEGACY-6) in
// `internal/api/images/handler_full.go` as a sibling of
// `ImagesHandler`. PR-IMG-LEGACY-6 retires that absorption: the
// handler now lives in its own canonical package
// `internal/api/fullimages/handler.go`, mounted on the
// /api/fullimages/* prefix (matches the gen_api_docs.go entry that
// has been the canonical URL since PR3 Wave 14, June 2026).
//
// godlike/06 SSOT (one canonical owner per fact): the
// `internal/api/fullimages/` package is the SOLE owner of the
// public fullimages wire-shape (FullImagesRequest / FullImagesResponse
// / FullImagesHandler / ErrEngineRetired). The composition root is
// the SOLE owner of the wire-up of the application-layer service
// to the HTTP handler. No other package re-exports these types.
//
// godlike/07 no-fake-availability: the 4 mandatory gates are checked
// UPFRONT in WireFullImages (ImageService / FfmpegProc / Publisher /
// ImagesDir are the 4 wiring deps; nil on any yields a typed error
// which the caller — registerInternalModules Step 7 — downgrades to
// log.Warn + skip-route + return-nil). Composition boot MUST NOT
// abort because FullImages is optional in the architecture (matches
// the godlike/07 fail-closed-at-composition contract applied to
// Artlist + StockPipeline + YouTubeClip).
//
// Single-function shape (WireFullImages) mirrors the existing
// WireArtlist precedent in build_bundles_artlist.go (Blocco C1-Step 3
// scope) and the WireMediaIngest / WireYouTubeClip / WireStockPipeline
// precedent in the same package. The FASE 6 (June 2026) wire-stub
// that lived at registerInternalModules Step 7 is REPLACED with the
// canonical re-introduction.
package app

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	fullimagesapi "github.com/Marcuss-ops/PipelineGen/internal/api/fullimages"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	mediafullimages "github.com/Marcuss-ops/PipelineGen/internal/application/images/fullimages"
	ffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

// FullImagesBundle is the capability bundle for the FullImages module.
//
// PR-IMG-LEGACY-6 (2026-07-06, CUTOVER phase): the 4 wiring deps
// (ImageService / FfmpegProc / Publisher / ImagesDir) match the
// signature of mediafullimages.NewService. The composition root
// owns the resolution of root.Domains.ImageService + cfg.Storage
// (for the ImagesDir path) + root.Drive.Publisher — these are the
// canonical cross-bundle reads that WireFullImages threads through
// the bundle.
type FullImagesBundle struct {
	ImageService *imgservice.Service
	FfmpegProc   *ffmpeg.Processor
	Publisher    delivery.Publisher
	ImagesDir    string
}

// WireFullImages constructs the FullImages module wiring (handler +
// module) from the canonical FullImagesBundle. Returns a typed
// *FullImagesWiring on success; nil + typed error on any of the 4
// mandatory gates (godlike/07 fail-closed-at-composition).
//
// The FASE 6 (June 2026) wire-stub at registerInternalModules Step 7
// (which set wiring.FullImages=nil + log.Warn) is REPLACED with this
// canonical re-introduction. The pre-FASE-6 WireFullImages helper
// (which lived at the same Step 7 prior to FASE 6 retirement) is
// also superseded by this version.
//
// enabledFunc: the route module is gated on `cfg.Features.ImagesEnabled`
// (the operator feature flag for the images capability family, of
// which fullimages is a subset) — matches the artlist pattern at
// WireArtlist's api.NewRouteModule call site. This lets operators
// disable the fullimages route at boot without code changes.
//
// Forward-pointers (godlike/07 honest scope-lock):
//   - PR-FULLIMAGES-FFMPEG-REUSE: WireFullImages constructs a fresh
//     *ffmpeg.Processor via ffmpeg.NewFromConfig(cfg) internally. A
//     second ffmpeg.Processor instance is constructed at
//     internal/app/registry_helpers.go:96 for the media processor.
//     Future work should thread the *ffmpeg.Processor through the
//     FullImagesBundle (or via the canonical *ComposeRoot) so both
//     consumers share one instance.
func WireFullImages(bundle *FullImagesBundle, cfg *config.Config, log *zap.Logger) (*FullImagesWiring, error) {
	if cfg == nil {
		return nil, fmt.Errorf("WireFullImages: cfg is nil")
	}
	// godlike/07 fail-closed: 4 mandatory gates UPFRONT. nil on any
	// yields a typed error which the caller (registerInternalModules
	// Step 7) downgrades to log.Warn + skip-route + return-nil. The
	// check order matches the constructor-arg order of
	// mediafullimages.NewService (ImageService, FfmpegProc, Publisher,
	// ImagesDir, log) so the diagnostic message maps 1:1 to the
	// missing arg.
	if bundle == nil {
		return nil, fmt.Errorf("WireFullImages: bundle is nil")
	}
	if bundle.ImageService == nil {
		return nil, fmt.Errorf("WireFullImages: bundle.ImageService is nil (GenerateSmartImage dep)")
	}
	if bundle.FfmpegProc == nil {
		return nil, fmt.Errorf("WireFullImages: bundle.FfmpegProc is nil (ImageToVideo dep)")
	}
	if bundle.Publisher == nil {
		return nil, fmt.Errorf("WireFullImages: bundle.Publisher is nil (P0-2 mandatory; nil publisher fail-closed at /upload)")
	}
	if bundle.ImagesDir == "" {
		return nil, fmt.Errorf("WireFullImages: bundle.ImagesDir is empty (Storage.ImagesPath() unresolved)")
	}

	// godlike/06 SSOT: the application-layer service is the canonical
	// SOLE owner of the section → video generation logic
	// (GenerateForSections). Composition root is the SOLE owner of
	// the DI wiring (the 4 deps → service ctor args).
	svc := mediafullimages.NewService(
		bundle.ImageService,
		bundle.FfmpegProc,
		bundle.Publisher,
		bundle.ImagesDir,
		log,
	)

	// godlike/06 SSOT: the public HTTP handler is the canonical SOLE
	// owner of the wire-shape (FullImagesRequest / FullImagesResponse
	// / FullImagesHandler / ErrEngineRetired) — all 4 live in
	// internal/api/fullimages/handler.go.
	handler := fullimagesapi.NewFullImagesHandler(svc)

	// godlike/06 SSOT: api.NewRouteModule is the canonical SOLE
	// owner of the route-publication contract. The prefix is
	// /api/fullimages (NOT /api/images) so the resulting public
	// URL is /api/fullimages/video/generate (matches the canonical
	// gen_api_docs.go entry). The enabledFunc gates the route on
	// cfg.Features.ImagesEnabled (ImagesEnabled is the canonical
	// feature flag for the images capability family, of which
	// fullimages is a subset). Mirrors WireArtlist's pattern at
	// the api.NewRouteModule call site in build_bundles_artlist.go.
	mod := api.NewRouteModule(
		"fullimages",
		func() bool { return cfg.Features.ImagesEnabled },
		"/api/fullimages",
		handler,
		log,
	)

	log.Info("WireFullImages: fullimages module wired",
		zap.String("route_prefix", "/api/fullimages"),
		zap.String("public_url", "/api/fullimages/video/generate"),
		zap.Bool("godlike_07_fail_closed", true),
	)

	return &FullImagesWiring{
		Module: mod,
	}, nil
}
