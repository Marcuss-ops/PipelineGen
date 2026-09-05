package generation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	imagestyles "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/styles"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

type JobHandler struct {
	registry *Registry
	styles   imagestyles.StyleResolver
	log      *zap.Logger
}

func NewJobHandler(registry *Registry, styles imagestyles.StyleResolver, log *zap.Logger) *JobHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &JobHandler{registry: registry, styles: styles, log: log}
}

type imageGeneratePayload struct {
	BatchID  string   `json:"batch_id"`
	Position int      `json:"position"`
	Prompt   string   `json:"prompt"`
	Style    string   `json:"style,omitempty"`
	Width    int      `json:"width"`
	Height   int      `json:"height"`
	Tags     []string `json:"tags,omitempty"`
}

func (h *JobHandler) HandleJob(ctx context.Context, j *job.Job, tools *jobs.JobTools) (map[string]any, error) {
	if h == nil {
		return nil, fmt.Errorf("image generation job handler is nil")
	}
	h.log.Info("handling image.generate.google job", zap.String("job_id", j.ID), zap.String("correlation_id", j.CorrelationID))
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
	if tools != nil && tools.Progress != nil {
		tools.Progress(5, "Starting image generation via Chrome/Playwright")
	}
	out, err := RunUsage(ctx, UsecaseDeps{Registry: h.registry, Styles: h.styles, Log: h.log}, UsecaseCommand{
		JobID: j.ID, Position: payload.Position, Prompt: payload.Prompt, Style: payload.Style,
		Width: payload.Width, Height: payload.Height, Tags: payload.Tags, OutputPath: outputPath,
	})
	if err != nil {
		h.log.Error("image.generate.google: usecase failed", zap.String("job_id", j.ID), zap.String("prompt", payload.Prompt), zap.String("artifact_file_path", outputPath), zap.Error(err))
		if tools != nil && tools.Progress != nil {
			tools.Progress(50, fmt.Sprintf("Image generation failed: %v", err))
		}
		return nil, err
	}
	if tools != nil && tools.Progress != nil {
		tools.Progress(95, "Building ArtifactManifest sidecar (C11 P0 Commit 11)")
	}
	result := map[string]any{"batch_id": payload.BatchID, "position": payload.Position, "prompt": payload.Prompt, "style": payload.Style, "path_rel": out.OutputPath}
	result[job.ManifestKey] = out.Manifest
	if tools != nil && tools.Progress != nil {
		tools.Progress(100, "Image generation completed (finalizer handles ingest via ArtifactManifest sidecar)")
	}
	return result, nil
}

func (h *JobHandler) RegisterHandler(jobsSvc *jobs.Service) error {
	if jobsSvc == nil {
		return fmt.Errorf("images.JobHandler.RegisterHandler: jobsSvc is nil (composition root must wire jobs.Service before calling Register): %w", jobs.ErrMissingDeps)
	}
	if err := jobsSvc.RegisterHandler(jobs.TypeImageGenerateGoogle, jobs.HandlerFunc(h.HandleJob)); err != nil {
		return fmt.Errorf("images.JobHandler.RegisterHandler: bind %q to dispatcher: %w", jobs.TypeImageGenerateGoogle, err)
	}
	h.log.Info("registered image.generate.google job handler")
	return nil
}
