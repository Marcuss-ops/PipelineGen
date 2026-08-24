// Package images (application/images) — service_generated.go holds
// the AI-generation methods on Service. Per PR-IMG-SPLIT-4 (July 2026),
// generated = AI-created images. This file owns:
//   - GenerateSmartImage  (sync AI generation)
//   - TriggerPrewarm      (warm-up Chrome worker)
//   - HandleJob           (async job delegation)
//   - RegisterHandler     (job handler registration)
//   - UploadBatchMetadata (metadata for batch generations)
//   - ErrImageGenNotImplemented (typed sentinel)
//
// Golden rule: generated territory methods never touch retrieved
// providers or storage/search logic directly.
package images

import (
	"context"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ErrImageGenNotImplemented is the typed sentinel returned when the
// image generation pipeline is not wired (no Chrome/Google Slides provider).
var ErrImageGenNotImplemented = fmt.Errorf("image generation via Google Slides is not configured")

// GenerateSmartImage delegates synchronous AI image generation to the
// held GenerationService. Returns ErrImageGenNotImplemented when the
// Gen sub-service is nil.
func (s *Service) GenerateSmartImage(ctx context.Context, subject, topic, style string, prompts []string, tags []string, width, height int, model string, skipDrive bool) (*asset.ImageAsset, error) {
	if s == nil || s.Gen == nil {
		return nil, ErrImageGenNotImplemented
	}
	return s.Gen.GenerateSmartImage(ctx, subject, topic, style, prompts, tags, width, height, model, skipDrive)
}

// GenerateArtifact runs the canonical registry-backed generation use case
// without ingesting the result. Callers that own a larger workflow (such as
// VidRush) receive the ArtifactManifest and must pass the staged artifact to
// the shared finalizer; this method deliberately does not write media_assets.
func (s *Service) GenerateArtifact(ctx context.Context, jobID, prompt, style string, width, height int, tags []string) (*UsecaseOutput, error) {
	if s == nil || s.Gen == nil {
		return nil, ErrImageGenNotImplemented
	}
	return RunUsage(ctx, UsecaseDeps{Registry: s.Gen.registry, Styles: s.Gen.styles, Log: s.Gen.log}, UsecaseCommand{
		JobID: jobID, Prompt: prompt, Style: style, Width: width, Height: height,
		Tags: tags, OutputPath: "",
	})
}

// TriggerPrewarm warms up the Chrome/Playwright worker subprocess
// ahead of a batch generation run.
func (s *Service) TriggerPrewarm(ctx context.Context, jobID string, count int) {
	s.Gen.TriggerPrewarm(ctx, jobID, count)
}

// HandleJob delegates to the held JobHandler (constructed once at
// NewService time per PR-IMAGES-SHIM-REMOVAL, 2026-07-04). The
// pre-removal pattern of constructing a fresh NewJobHandler(...)
// per call is RETIRED — composition root owns the canonical wiring.
func (s *Service) HandleJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	if s == nil || s.JobHandler == nil {
		return nil, fmt.Errorf("images.Service.HandleJob: JobHandler not wired (composition must call NewService): %w", appjobs.ErrMissingDeps)
	}
	return s.JobHandler.HandleJob(ctx, j, tools)
}

// RegisterHandler registers the image generation job handler with the
// async job system.
func (s *Service) RegisterHandler(jobsSvc *appjobs.Service) error {
	if jobsSvc == nil {
		return fmt.Errorf("images.Service.RegisterHandler: jobsSvc is nil: %w", appjobs.ErrMissingDeps)
	}
	if s.JobHandler == nil {
		return fmt.Errorf("images.Service.RegisterHandler: JobHandler not wired (composition must call NewService): %w", appjobs.ErrMissingDeps)
	}
	if err := s.JobHandler.RegisterHandler(jobsSvc); err != nil {
		return fmt.Errorf("images.Service.RegisterHandler: %w", err)
	}
	return nil
}

// UploadBatchMetadata uploads metadata for a batch of generated images
// via the Meta sub-service.
func (s *Service) UploadBatchMetadata(ctx context.Context, genID, slug, style, prompt, generator string, imageAssets []*asset.ImageAsset) {
	s.Meta.UploadBatchMetadata(ctx, genID, slug, style, prompt, generator, imageAssets)
}
