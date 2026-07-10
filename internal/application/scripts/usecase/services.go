// Package scripts — service interfaces extracted from types.go (PG-029, June 2026).
//
// Fase 9 step 2 (Spina Dorsale, July 2026): the two translation
// service interfaces (TextTranslationService + TranslatorService)
// are now Go type-aliases for the canonical godlike/06 SSOT
// declarations in internal/application/translation/legacy.go
// (godlike/07 EXPAND window + alias-before-removal pattern). The
// new canonical concrete OllamaTranslator
// (internal/application/translation/ollama_translator.go) satisfies
// TranslationPort + all 3 legacy aliases, so production wiring
// constructs one concrete and routes every consumer through it.
//
// A new `TranslationPort translation.TranslationPort` field is
// added to ClipServices; see services.go::artlistSearchPhrase in
// flow_helpers.go for the first migrated caller. Other callers
// continue to use the legacy `Translation` / `Translator` fields
// during the godlike/07 EXPAND window.
package usecase

import (
	"context"

	translation "github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	domain "github.com/Marcuss-ops/PipelineGen/internal/domain/voiceover"
	"go.uber.org/zap"
)

// ── ClipServices ─────────────────────────────────────────────────────────

// ClipServices bundles all service dependencies for clip-related functions.
type ClipServices struct {
	ClipSearch  ClipSearchService
	Association AssociationService
	DriveCheck  DriveCheckService
	ImageSearch ImageSearchService
	Translation TextTranslationService
	JobEnqueue  JobEnqueueService
	Harvest     HarvestService
	Voiceover   VoiceoverService
	RealtimeSvc RealtimeSearchService
	HarvestSvc  HarvestService
	Logger      *zap.Logger
	Translator  TranslatorService
	// TranslationPort is the canonical Fase 9 step 2 surface. New
	// consumers (e.g. flow_helpers.go::artlistSearchPhrase) call
	// svc.TranslationPort.Translate(ctx, cmd) directly. The legacy
	// `Translation` + `Translator` fields above stay populated for
	// the godlike/07 EXPAND window and continue to satisfy existing
	// callers until CUTOVER. Nil is tolerated by every caller (each
	// is guarded against nil before invocation).
	TranslationPort translation.TranslationPort
	MetadataModel   string
	AssocSvc        AssocSearchService
	DriveSvc        DriveCheckService
	JobsSvc         JobEnqueueService
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

// DriveCheckService narrows drive check operations.
type DriveCheckService interface {
	FileIsNotTrashed(ctx context.Context, fileID string) (bool, error)
}

// ImageSearchService narrows image search operations.
type ImageSearchService interface {
	Search(ctx context.Context, query string, limit int) ([]any, error)
}

// TextTranslationService is now a Go type alias for the canonical
// Fase 9 godlike/06 SSOT declaration in
// internal/application/translation/legacy.go (byte-stable
// signature; legacy 3-arg straggler renamed method from Translate
// to TranslateText to match *ollama.Generator.TranslateText and
// avoid name-collision with translation.TranslationPort.Translate).
//
// Reference path: composition root constructs *OllamaTranslator and
// populates svc.Translation on ClipServices. Existing 3-arg callers
// compile unchanged via alias.
type TextTranslationService = translation.LegacyTextTranslationService

// JobEnqueueService narrows job enqueue operations.
type JobEnqueueService interface {
	Enqueue(ctx context.Context, req any) (any, error)
}

// HarvestService narrows harvest operations.
type HarvestService interface {
	EnqueueHarvest(ctx context.Context, req any, maxClips int, profile string) (any, error)
}

// RealtimeSearchService narrows realtime search operations.
type RealtimeSearchService interface {
	SearchClips(ctx context.Context, query, source, mediaType string, limit int, minScore float64) ([]RealtimeMatchAsset, error)
}

// TranslatorService is now a Go type alias for the canonical
// Fase 9 godlike/06 SSOT declaration in
// internal/application/translation/legacy.go (byte-stable 4-arg
// signature; matches *ollama.Generator.TranslateTextWithModel
// and *OllamaTranslator.TranslateTextWithModel shapes).
//
// Deprecated in the godlike/07 EXPAND-window sense: new consumers
// migrate to translation.TranslationPort. The legacy field stays
// populated on ClipServices and the alias stays in place until the
// CUTOVER-phase removal (tracking entry
// architecture/deprecations.yaml#TRANSLATION-LEGACY-SERVICES-MIGRATION).
type TranslatorService = translation.LegacyTranslatorService

// AssocSearchService narrows association search operations with typed request/response.
type AssocSearchService interface {
	BuildCandidates(ctx context.Context, req AssociationCandidatesRequest) (*AssociationCandidatesResponse, error)
}

// ImageGenService narrows image search + generation operations.
//
// PR C8 (July 2026): the `extra any` zombie parameter was
// removed from SearchAndDownload. Both production callers
// (internal/application/scripts/usecase/flow_helpers.go::enrichSingleEntity
// and internal/application/scripts/adapters/processor_images.go::Process)
// historically passed `nil`; the only file with a non-`nil` type-assertion
// path was internal/app/wire_script_curation.go::imageGenSvcAdapter,
// where the `extra.([]string)` cast branches into a `tags` arg for
// GenerateSmartImage. That cast NEVER fired (no caller passed non-nil
// extra at composition time), so the entire any channel was
// untraffic — dropping it preserves byte-equivalent behaviour and
// satisfies godlike/06 SSOT (no operator-of-untyped-traffic).
type ImageGenService interface {
	SearchAndDownload(ctx context.Context, name, description, query, language string) (*asset.ImageAsset, error)
	GenerateSmartImage(ctx context.Context, name, description, style string, prompts, tags []string, width, height int, extra string, flag bool) (*asset.ImageAsset, error)
}

// VoiceoverService narrows voiceover operations.
// PR 5 (June 2026): typed port — takes domain.GenerateVoiceoverCommand,
// returns *domain.VoiceoverResult. No more any.
type VoiceoverService interface {
	Generate(ctx context.Context, cmd domain.GenerateVoiceoverCommand) (*domain.Result, error)
}
