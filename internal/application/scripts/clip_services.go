// Package scripts — ClipServices dependency bundle and narrow port interfaces.
//
// PR2 (June 2026): extracted from internal/api/script/helpers.go so
// the application layer can own the dependency contracts. The API
// layer re-exports via type aliases for zero-churn back-compat.
package scripts

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/association"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/realtime"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"

	"go.uber.org/zap"
)

// ── Narrow service interfaces (application-layer ports) ─────────────────

// ClipSearchService narrows realtime.MatchAsset search.
type ClipSearchService interface {
	SearchClips(ctx context.Context, query, source, mediaType string, limit int, minScore float64) ([]realtime.MatchAsset, error)
}

// AssociationService narrows association.CandidatesRequest building.
type AssociationService interface {
	BuildCandidates(ctx context.Context, req association.CandidatesRequest) (*association.CandidatesResponse, error)
}

// DriveCheckService narrows drive.Uploader.FileIsNotTrashed.
type DriveCheckService interface {
	FileIsNotTrashed(ctx context.Context, fileID string) (bool, error)
}

// ImageSearchService narrows images.Service ingest + generation.
type ImageSearchService interface {
	SearchAndDownload(ctx context.Context, subjectSlug, displayName, query, lang string, tags []string) (*asset.ImageAsset, error)
	GenerateSmartImage(ctx context.Context, subject, topic, style string, prompts, tags []string, width, height int, model string, skipDrive bool) (*asset.ImageAsset, error)
	TriggerPrewarm(ctx context.Context, jobID string, count int)
}

// TextTranslationService narrows ollama.Generator.TranslateTextWithModel.
type TextTranslationService interface {
	TranslateTextWithModel(ctx context.Context, text, targetLanguage, model string) (string, error)
}

// JobEnqueueService narrows job.Service.Enqueue.
type JobEnqueueService interface {
	Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error)
}

// HarvestService narrows AutoHarvestService.EnqueueHarvest.
type HarvestService interface {
	EnqueueHarvest(ctx context.Context, term string, limit int, preset string) (string, error)
}

// VoiceoverService narrows voiceover.Service.GenerateWithDestination.
type VoiceoverService interface {
	GenerateWithDestination(ctx context.Context, text, language, filename string, dest *voiceover.DestinationRequest) (*voiceover.VoiceoverResult, error)
}

// ── ClipServices dependency bundle ──────────────────────────────────────

// ClipServices bundles all service dependencies for standalone clip-related
// functions in the scripts application package. Passed to functions like
// SearchScriptAssets, SearchArtlistClips, etc.
type ClipServices struct {
	Logger        *zap.Logger
	RealtimeSvc   ClipSearchService
	AssocSvc      AssociationService
	DriveSvc      DriveCheckService
	Translator    TextTranslationService
	JobsSvc       JobEnqueueService
	ImgSvc        ImageSearchService
	VoSvc         VoiceoverService
	HarvestSvc    HarvestService
	ArtlistFolder string // root Drive folder ID for Artlist downloads
	MetadataModel string // lightweight model for metadata/translation tasks
}
