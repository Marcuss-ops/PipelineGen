package api

import (
	"context"

	"go.uber.org/zap"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/media/association"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/internal/media/realtime"
	"github.com/Marcuss-ops/PipelineGen/internal/media/voiceover"
)

// ── Service Interfaces ───────────────────────────────────────────────────────

// ClipSearchService searches for clips by query.
type ClipSearchService interface {
	SearchClips(ctx context.Context, query, source, mediaType string, limit int, minScore float64) ([]realtime.MatchAsset, error)
}

// AssociationService builds candidates for folder recommendations.
type AssociationService interface {
	BuildCandidates(ctx context.Context, req association.CandidatesRequest) (*association.CandidatesResponse, error)
}

// DriveCheckService checks if a Drive file/folder is valid.
type DriveCheckService interface {
	FileIsNotTrashed(ctx context.Context, fileID string) (bool, error)
}

// ImageSearchService searches for and generates images.
type ImageSearchService interface {
	SearchAndDownload(ctx context.Context, subjectSlug, displayName, query, lang string, tags []string) (*models.ImageAsset, error)
	GenerateSmartImage(ctx context.Context, subject, topic, style string, prompts, tags []string, width, height int, model string, skipDrive bool) (*models.ImageAsset, error)
	TriggerPrewarm(ctx context.Context, jobID string, count int)
}

// TextTranslationService translates text between languages.
type TextTranslationService interface {
	TranslateTextWithModel(ctx context.Context, text, targetLanguage, model string) (string, error)
}

// JobEnqueueService enqueues background jobs.
type JobEnqueueService interface {
	Enqueue(ctx context.Context, req *jobservice.EnqueueRequest) (*jobservice.Job, error)
}

// HarvestService enqueues clip harvest jobs.
type HarvestService interface {
	EnqueueHarvest(ctx context.Context, term string, limit int, preset string) (string, error)
}

// ── ClipServices ─────────────────────────────────────────────────────────────

// ClipServices bundles all service dependencies for standalone clip-related
// functions in the script handlers package. Passed as a single struct to
// functions like SearchScriptAssets, SearchArtlistClips, etc.
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

// VoiceoverService interface for voiceover generation.
type VoiceoverService interface {
	GenerateWithDestination(ctx context.Context, text, language, filename string, dest *voiceover.DestinationRequest) (*voiceover.VoiceoverResult, error)
}
