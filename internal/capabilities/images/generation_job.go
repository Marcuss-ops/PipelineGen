// Package images — generation_job.go: async job adapter for
// image.generate.google (PR-GODOBJ-3-IMAGES-GENERATION, July 2026).
//
// godlike/06 SSOT: canonical owner of the ASYNC job adapter for image
// generation. The handler is thin — it drives the usecase and emits the
// typed *job.ArtifactManifest sidecar via handlerResult[job.ManifestKey].
// Per PR-GODOBJ-3 KILL LIST b: the handler does NOT call IngestImage
// (no media_assets writes, no asset.Hash / asset.PathRel in handlerResult).
// The runner's finalizer reads the manifest via job.Decode and owns the
// Sender-side upload + media_assets persistence cycle.
//
// PR-GODOBJ-3 KILL LIST applied:
//
//	(a) No legacy imageGen.Generate fallback — dispatch goes through
//	    registry ONLY via generation_usecase.RunUsage.
//	(b) No inline IngestImage call — the runner's finalizer handles
//	    persistence through the manifest sidecar (per KILL LIST b:
//	    the finalizer is the SOLE owner of media_assets persistence
//	    for image-generation async paths).
//	(c) GenerateSmartImageWithAccount REMOVED — the job payload's
//	    typed shape (imageGeneratePayload) has NO account/project fields.
//
// godlike/07 honest-limitation disclosure (AGENTS.md Check 44 LoC cap):
// This file exceeds the 66-LoC transitional cap (~120 LoC) because the
// job payload struct + handler orchestration + register call form an
// inherently verbose adapter. Forward-pointer linked_issue:
// PR-GODOBJ-3c-JOB-SLIM extracts the result-map builder into a thin
// runtime helper (≤30 LoC).
package images

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/workflow/generated"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// JobHandler is the canonical async adapter for image.generate.google
// jobs. Composition wires a single JobHandler per worker; the handler
// binds via RegisterHandler to appjobs.Dispatcher.
type JobHandler struct {
	registry *generated.GenerationProviderRegistry
	styles   generation.StyleResolver
	log      *zap.Logger
}

// NewJobHandler constructs the canonical async adapter (composition
// root helper; not exported beyond the package).
func NewJobHandler(registry *generated.GenerationProviderRegistry, styles generation.StyleResolver, log *zap.Logger) *JobHandler {
	return &JobHandler{registry: registry, styles: styles, log: log}
}

// imageGeneratePayload is the job payload shape — JSON-decoded by the
// broker supervisor before HandleJob is invoked. PR-GODOBJ-3 KILL
// LIST c: NO account/project fields on this struct.
type imageGeneratePayload struct {
	BatchID  string   `json:"batch_id"`
	Position int      `json:"position"`
	Prompt   string   `json:"prompt"`
	Style    string   `json:"style,omitempty"`
	Width    int      `json:"width"`
	Height   int      `json:"height"`
	Tags     []string `json:"tags,omitempty"`
}

// HandleJob processes an image.generate.google job. KILL LIST applied:
//
//	(a) Dispatch goes through the registry ONLY via RunUsage (NO imageGen
//	    fallback). A nil registry produces ErrNoGenerationProviderWired.
//	(b) The handler returns ONLY (payload + manifest sidecar). NO inline
//	    IngestImage — the runner's finalizer handles persistence via
//	    handlerResult[job.ManifestKey].Finalizer handles media_assets.
func (h *JobHandler) HandleJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	h.log.Info("handling image.generate.google job",
		zap.String("job_id", j.ID),
		zap.String("correlation_id", j.CorrelationID),
	)

	// FASE 4(b) (July 2026): cancel signal is observed via ctx.Err()
	// rather than the pre-Fase-4 tools.IsCancelled callback (which
	// polled a 2-second cancel-watcher goroutine REMOVED in FASE 4(b)).
	// The typed job.RenewLeaseResult.State (CancelRequested)
	// → renewLeaseLoopWith calls jobCancel(jobCtx) → ctx.Done().
	if ctx.Err() != nil {
		return nil, fmt.Errorf("image.generate.google: job %s was cancelled before execution started", j.ID)
	}

	var payload imageGeneratePayload
	if err := json.Unmarshal(j.Payload, &payload); err != nil {
		return nil, fmt.Errorf("image.generate.google: failed to unmarshal job payload: %w", err)
	}

	if payload.Prompt == "" {
		return nil, fmt.Errorf("image.generate.google: empty prompt in job payload (job_id=%s)", j.ID)
	}

	if payload.Width == 0 {
		payload.Width = 1920
	}
	if payload.Height == 0 {
		payload.Height = 1080
	}

	workspaceDir := filepath.Join("/tmp", "pipelinegen", "jobs", j.ID)
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		return nil, fmt.Errorf("image.generate.google: failed to create workspace dir %s: %w", workspaceDir, err)
	}
	outputPath := filepath.Join(workspaceDir, "output.png")

	if tools.Progress != nil {
		tools.Progress(5, "Starting image generation via Chrome/Playwright")
	}

	// KILL LIST (b) applied: NO IngestImage in this handler. The runner
	// finalizer handles media_assets persistence through the
	// ArtifactManifest sidecar (handlerResult[job.ManifestKey]).
	out, err := RunUsage(ctx, UsecaseDeps{
		Registry: h.registry,
		Styles:   h.styles,
		Log:      h.log,
	}, UsecaseCommand{
		JobID:      j.ID,
		Position:   payload.Position,
		Prompt:     payload.Prompt,
		Style:      payload.Style,
		Width:      payload.Width,
		Height:     payload.Height,
		Tags:       payload.Tags,
		OutputPath: outputPath,
	})
	if err != nil {
		h.log.Error("image.generate.google: usecase failed",
			zap.String("job_id", j.ID),
			zap.String("prompt", payload.Prompt),
			zap.String("artifact_file_path", outputPath),
			zap.Error(err),
		)
		if tools.Progress != nil {
			tools.Progress(50, fmt.Sprintf("Image generation failed: %v", err))
		}
		return nil, err
	}

	if tools.Progress != nil {
		tools.Progress(95, "Building ArtifactManifest sidecar (C11 P0 Commit 11)")
	}

	// handlerResult carries the canonical payload + the typed manifest
	// sidecar under job.ManifestKey. The finalizer (worker.Runner) reads
	// the manifest via job.Decode and uploads each artifact through the
	// Sender-side delivery port. Persisted media_assets rows are NOT
	// created here (KILL LIST b: finalizer is the sole owner of
	// media_assets persistence for image-generation async paths).
	handlerResult := map[string]any{
		"batch_id": payload.BatchID,
		"position": payload.Position,
		"prompt":   payload.Prompt,
		"style":    payload.Style,
		"path_rel": out.OutputPath,
	}
	handlerResult[job.ManifestKey] = out.Manifest

	if tools.Progress != nil {
		tools.Progress(100, "Image generation completed (finalizer handles ingest via ArtifactManifest sidecar)")
	}
	return handlerResult, nil
}

// RegisterHandler registers the image.generate.google handler with the
// job service. Called from composition.go late-bindings.
//
// Register propagates wiring errors — composition root MUST fail-closed
// on non-nil return.
func (h *JobHandler) RegisterHandler(jobsSvc *appjobs.Service) error {
	if jobsSvc == nil {
		return fmt.Errorf("images.JobHandler.RegisterHandler: jobsSvc is nil (composition root must wire jobs.Service before calling Register): %w", appjobs.ErrMissingDeps)
	}
	if err := jobsSvc.RegisterHandler(appjobs.TypeImageGenerateGoogle, appjobs.HandlerFunc(h.HandleJob)); err != nil {
		return fmt.Errorf("images.JobHandler.RegisterHandler: bind %q to dispatcher: %w", appjobs.TypeImageGenerateGoogle, err)
	}
	h.log.Info("registered image.generate.google job handler")
	return nil
}
