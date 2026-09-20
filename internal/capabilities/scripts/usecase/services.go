// Package scripts — service interfaces extracted from types.go (PG-029, June 2026).
//
// PR-DEADC-SCRIPTS-CLIP-SERVICES-PER-USE-CASE-DEP-BAGS Step 1+2 (July 2026):
// retired 6 dual-field legacy surfaces from ClipServices
// (Association + DriveCheck + Translation + JobEnqueue + Harvest +
// Translator) + the dead MetadataModel field (wired at composition
// time but with 0 readers in any use-case site). The canonical
// Fase 9 step-2 surface TranslationPort remains the SOLE owner of
// the translation contract.
//
// CLIPSERVICES DRIVE/JOBS RETIREMENT (2026-09-20): DriveSvc and JobsSvc were
// the remaining two fields of that same legacy fallback, and they are now gone
// too. The July note above described them as canonical Modern-side owners, but
// the follow-up change that retired the ClipServices postprocessor path
// (wire_script_postprocess_ai.go: "the old ClipServices/Drive/Jobs/remote
// fallback is intentionally gone") removed their last reader, leaving two
// fields + two interfaces whose only implementers (a composition-root
// driveCheckServiceAdapter and jobsEnqueueServiceAdapter) were never
// constructed. AssocSvc + HarvestSvc remain, and they do have readers.
package usecase

import (
	"context"

	translation "github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
)

// ── ClipServices ─────────────────────────────────────────────────────────

// ClipServices bundles all service dependencies for clip-related functions.
//
// Post-PR-DEADC-SCRIPTS-CLIP-SERVICES-PER-USE-CASE-DEP-BAGS Step 1+2 (July 2026):
// the 6 dual-field legacy surfaces (Association + DriveCheck + Translation +
// JobEnqueue + Harvest + Translator) are RETIRED. The Modern-side
// counterparts (AssocSvc + DriveSvc + TranslationPort + JobsSvc + HarvestSvc)
// are the canonical owners of their respective contracts.
type ClipServices struct {
	ClipSearch  ClipSearchService
	ImageSearch ImageSearchService
	RealtimeSvc RealtimeSearchService
	HarvestSvc  HarvestService
	Logger      *zap.Logger
	// TranslationPort is the canonical Fase 9 step 2 surface. New
	// consumers (e.g. flow_helpers_artlist.go::phraseTranslatorAdapter.Translate)
	// call svc.TranslationPort.Translate(ctx, cmd) directly. Nil is
	// tolerated by the caller (fail-closed: returns typed error per
	// godlike/07 NO-FAKE-AVAILABILITY — never a silent fallback to the
	// original phrase).
	TranslationPort translation.TranslationPort
	AssocSvc        AssocSearchService
	ArtlistFolder   string
	ImgSvc          ImageGenService
}

// ── Service interfaces ───────────────────────────────────────────────────

// ClipSearchService narrows clip search operations.
type ClipSearchService interface {
	EmbedTextForVector(ctx context.Context, text, vectorName string) ([]float32, error)
}

// AssociationService narrows association operations.
type AssociationService interface {
	BuildCandidates(ctx context.Context, req any) (any, error)
}

// DriveCheckService was DELETED here on 2026-09-20 with ClipServices.DriveSvc:
// its only implementer was the unwired composition-root driveCheckServiceAdapter
// and the field had no reader left after the ClipServices postprocessor
// fallback was retired.

// ImageSearchService narrows image search operations.
type ImageSearchService interface {
	Search(ctx context.Context, query string, limit int) ([]any, error)
}

// JobEnqueueService was DELETED here on 2026-09-20 with ClipServices.JobsSvc:
// its only implementer was the unwired composition-root jobsEnqueueServiceAdapter
// and the field had no reader left after the ClipServices postprocessor
// fallback was retired.

// HarvestService narrows harvest operations.
type HarvestService interface {
	EnqueueHarvest(ctx context.Context, req any, maxClips int, profile string) (any, error)
}

// RealtimeSearchService narrows realtime search operations.
type RealtimeSearchService interface {
	SearchClips(ctx context.Context, query, source, mediaType string, limit int, minScore float64) ([]RealtimeMatchAsset, error)
}

// AssocSearchService narrows association search operations with typed request/response.
type AssocSearchService interface {
	BuildCandidates(ctx context.Context, req AssociationCandidatesRequest) (*AssociationCandidatesResponse, error)
}

// ImageGenService narrows image search + generation operations.
//
// PR C8 (July 2026): the `extra any` zombie parameter was
// removed from SearchAndDownload. Both production callers
// (internal/capabilities/scripts/usecase/flow_helpers.go::enrichSingleEntity
// and internal/capabilities/scripts/adapters/processor_images.go::Process)
// historically passed `nil`; the only file with a non-`nil` type-assertion
// path was internal/app/wire_script_curation.go::imageGenSvcAdapter,
// where the `extra.([]string)` cast branches into a `tags` arg for
// GenerateSceneImage (formerly GenerateSmartImage; renamed by
// PR-DEADC-IMAGES-IMAGE-GEN-SERVICE-INTERFACE-CONTRACT on 2026-07-10
// to disambiguate the script-layer usecase port from the canonical
// *images.Service.GenerateSmartImage production surface at
// internal/capabilities/images/service_generated.go). That cast NEVER
// fired (no caller passed non-nil extra at composition time), so the
// entire any channel was untraffic — dropping it preserves
// byte-equivalent behaviour and satisfies godlike/06 SSOT (no
// operator-of-untyped-traffic).
type ImageGenService interface {
	SearchAndDownload(ctx context.Context, name, description, query, language string) (*detail.ImageAsset, error)
	GenerateSceneImage(ctx context.Context, name, description, style string, prompts, tags []string, width, height int, extra string, flag bool) (*detail.ImageAsset, error)
}
