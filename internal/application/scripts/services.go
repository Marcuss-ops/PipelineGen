// Package scripts — service interfaces extracted from types.go (PG-029, June 2026).
package scripts

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"go.uber.org/zap"
)

// ── ClipServices ─────────────────────────────────────────────────────────

// ClipServices bundles all service dependencies for clip-related functions.
type ClipServices struct {
	ClipSearch    ClipSearchService
	Association   AssociationService
	DriveCheck    DriveCheckService
	ImageSearch   ImageSearchService
	Translation   TextTranslationService
	JobEnqueue    JobEnqueueService
	Harvest       HarvestService
	Voiceover     VoiceoverService
	RealtimeSvc   RealtimeSearchService
	HarvestSvc    HarvestService
	Logger        *zap.Logger
	Translator    TranslatorService
	MetadataModel string
	AssocSvc      AssocSearchService
	DriveSvc      DriveCheckService
	JobsSvc       JobEnqueueService
	ArtlistFolder string
	ImgSvc        ImageGenService
}

// ── Service interfaces ───────────────────────────────────────────────────

// ClipSearchService narrows clip search operations.
type ClipSearchService interface {
	EmbedTextForVector(ctx context.Context, text, vectorName string) ([]float32, error)
}

// AssociationService narrows association operations.
type AssociationService interface {
	BuildCandidates(ctx context.Context, req interface{}) (interface{}, error)
}

// DriveCheckService narrows drive check operations.
type DriveCheckService interface {
	FileIsNotTrashed(ctx context.Context, fileID string) (bool, error)
}

// ImageSearchService narrows image search operations.
type ImageSearchService interface {
	Search(ctx context.Context, query string, limit int) ([]interface{}, error)
}

// TextTranslationService narrows text translation operations.
type TextTranslationService interface {
	Translate(ctx context.Context, text, targetLang string) (string, error)
}

// JobEnqueueService narrows job enqueue operations.
type JobEnqueueService interface {
	Enqueue(ctx context.Context, req interface{}) (interface{}, error)
}

// HarvestService narrows harvest operations.
type HarvestService interface {
	EnqueueHarvest(ctx context.Context, req interface{}, maxClips int, profile string) (interface{}, error)
}

// RealtimeSearchService narrows realtime search operations.
type RealtimeSearchService interface {
	SearchClips(ctx context.Context, query, source, mediaType string, limit int, minScore float64) ([]RealtimeMatchAsset, error)
}

// TranslatorService narrows translator operations with model support.
type TranslatorService interface {
	TranslateTextWithModel(ctx context.Context, text, lang, model string) (string, error)
}

// AssocSearchService narrows association search operations with typed request/response.
type AssocSearchService interface {
	BuildCandidates(ctx context.Context, req AssociationCandidatesRequest) (*AssociationCandidatesResponse, error)
}

// ImageGenService narrows image search + generation operations.
type ImageGenService interface {
	SearchAndDownload(ctx context.Context, name, description, query, language string, extra interface{}) (*asset.ImageAsset, error)
	GenerateSmartImage(ctx context.Context, name, description, style string, prompts, tags []string, width, height int, extra string, flag bool) (*asset.ImageAsset, error)
}

// VoiceoverService narrows voiceover operations.
type VoiceoverService interface {
	Generate(ctx context.Context, text, language, filename string) (interface{}, error)
	// GenerateWithDestination routes the voiceover to a specific Drive
	// folder. When the plan carries VoiceoverFolderID or is resolved
	// from VoiceoverGroup, the processor passes a DestinationRequest
	// so the audio lands in the correct group folder instead of the
	// default voiceover root.
	GenerateWithDestination(ctx context.Context, text, language, filename string, dest *voiceover.DestinationRequest) (interface{}, error)
}
