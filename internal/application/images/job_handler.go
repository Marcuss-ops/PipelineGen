// Package images — job_handler.go implements the worker-side handler for
// image.generate.google jobs (FASE 3, June 2026).
//
// The handler is called by the worker system when a worker claims an
// image.generate.google job. It parses the payload, delegates to the
// injected ImageGenerator port, ingests the result, and reports progress.
//
// Current state (Step 6): the handler is wired but ChromeImageProvider is
// a stub (returns ErrImageGenNotImplemented). Real Playwright integration
// is pending FASE 7-8.
package images

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// imageGeneratePayload is the job payload shape for image.generate.google jobs.
type imageGeneratePayload struct {
	BatchID  string   `json:"batch_id"`
	Position int      `json:"position"`
	Prompt   string   `json:"prompt"`
	Style    string   `json:"style,omitempty"`
	Width    int      `json:"width"`
	Height   int      `json:"height"`
	Model    string   `json:"model,omitempty"`
	Tags     []string `json:"tags,omitempty"`
}

// HandleJob processes an image.generate.google job from the worker queue.
//
// FASE 9 (June 2026): canonical workspace-based ingest. The handler creates
// /tmp/pipelinegen/jobs/<job_id>/ and passes the output path directly to the
// ImageGenerator port. After generation, the file is ingested via
// ingestGeneratedImage → IngestImage → media_assets using the on-disk file
// (zero-copy), avoiding an in-memory byte copy.
func (s *Service) HandleJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	s.log.Info("handling image.generate.google job",
		zap.String("job_id", j.ID),
		zap.String("correlation_id", j.CorrelationID),
	)

	// Check cancellation before doing any work.
	if tools.IsCancelled != nil && tools.IsCancelled() {
		return nil, fmt.Errorf("image.generate.google: job %s was cancelled before execution started", j.ID)
	}

	var payload imageGeneratePayload
	if err := json.Unmarshal(j.Payload, &payload); err != nil {
		return nil, fmt.Errorf("image.generate.google: failed to unmarshal job payload: %w", err)
	}

	if payload.Prompt == "" {
		return nil, fmt.Errorf("image.generate.google: empty prompt in job payload (job_id=%s)", j.ID)
	}

	// Apply defaults
	if payload.Width == 0 {
		payload.Width = 1920
	}
	if payload.Height == 0 {
		payload.Height = 1080
	}

	// ── Idempotency note (FASE 10): SourceHash is computed inside
	// ChromeImageProvider.Generate() and flows through as genID to
	// ingestDirect for audit trail. True pre-generation idempotency
	// (skipping generation when a matching SourceHash exists) requires
	// a source_hash column in media_assets — pending migration.
	// Content-based dedup in IngestImage (SHA256 of image bytes)
	// catches duplicates post-hoc today.

	// ── Canonical workspace path (FASE 9) ────────────────────────────
	workspaceDir := filepath.Join("/tmp", "pipelinegen", "jobs", j.ID)
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		return nil, fmt.Errorf("image.generate.google: failed to create workspace dir %s: %w", workspaceDir, err)
	}
	outputPath := filepath.Join(workspaceDir, "output.png")

	if tools.Progress != nil {
		tools.Progress(5, "Starting image generation via Chrome/Playwright")
	}

	// ── Generate via ImageGenerator port (direct call, bypassing GenerateSmartImage) ──
	if s.imageGen == nil {
		return nil, fmt.Errorf("image.generate.google: ImageGenerator not wired")
	}

	result, err := s.imageGen.Generate(ctx, GenerateImageRequest{
		Prompt:     payload.Prompt,
		Style:      payload.Style,
		Width:      payload.Width,
		Height:     payload.Height,
		Model:      payload.Model,
		Tags:       payload.Tags,
		OutputPath: outputPath,
	})
	if err != nil {
		s.log.Error("image.generate.google: generation failed",
			zap.String("job_id", j.ID),
			zap.String("prompt", payload.Prompt),
			zap.String("output_path", outputPath),
			zap.Error(err),
		)
		return nil, fmt.Errorf("image generation failed: %w", err)
	}

	if tools.Progress != nil {
		tools.Progress(50, "Image generated, ingesting into media_assets")
	}

	// ── Ingest into media_assets via canonical file-based path ───────
	asset, err := s.ingestGeneratedImage(ctx, result, payload.Style, payload.Tags, false)
	if err != nil {
		s.log.Error("image.generate.google: ingest failed",
			zap.String("job_id", j.ID),
			zap.String("output_path", outputPath),
			zap.Error(err),
		)
		return nil, fmt.Errorf("image ingest failed: %w", err)
	}

	// Ingest succeeded — the canonical copy now lives in media_assets.
	// Clean up the workspace file (best-effort; /tmp is ephemeral anyway).
	_ = os.Remove(outputPath)

	if tools.Progress != nil {
		tools.Progress(100, "Image generation completed")
	}

	return map[string]any{
		"batch_id":       payload.BatchID,
		"position":       payload.Position,
		"prompt":         payload.Prompt,
		"style":          payload.Style,
		"asset_hash":     asset.Hash,
		"path_rel":       asset.PathRel,
		"drive_file_id":  asset.DriveFileID,
		"drive_link":     s.FormatDriveLink(asset.DriveFileID),
		"description":    asset.Description,
		"workspace_path": outputPath,
		"workspace_dir":  workspaceDir,
	}, nil
}

// RegisterHandler registers the image.generate.google handler with the
// job service. Called from composition.go late-bindings.
func (s *Service) RegisterHandler(jobsSvc *appjobs.Service) {
	if jobsSvc == nil {
		s.log.Warn("images.RegisterHandler: jobsSvc is nil — handler not registered")
		return
	}
	if err := jobsSvc.RegisterHandler(appjobs.TypeImageGenerateGoogle, s.HandleJob); err != nil {
		s.log.Error("failed to register image.generate.google handler", zap.Error(err))
		return
	}
	s.log.Info("registered image.generate.google job handler")
}
