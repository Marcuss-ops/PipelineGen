package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"velox/go-master/internal/core/jobs"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/models"

	"go.uber.org/zap"
)

// RegisterJobHandlers registers the handlers for script jobs
func (h *ScriptFlowHandler) RegisterJobHandlers(jobsSvc *jobservice.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(models.JobType(jobs.JobTypeBatchScriptGenerate), h.HandleBatchScriptGenerateJob)
		h.log.Info("registered script.generate_batch job handler")
		jobsSvc.RegisterHandler(models.JobType(jobs.JobTypeClipScriptGenerate), h.HandleClipScriptGenerateJob)
		h.log.Info("registered script.generate_from_clips job handler")
		jobsSvc.RegisterHandler(models.JobType(jobs.JobTypeCatalogScriptGenerate), h.HandleCatalogScriptGenerateJob)
		h.log.Info("registered script.generate_from_catalog job handler")
		jobsSvc.RegisterHandler(models.JobType(jobs.JobTypeMediaCurate), h.HandleCurateJob)
		h.log.Info("registered script.curate job handler")
	}
}

// HandleBatchScriptGenerateJob processes the background job for script.generate_batch
func (h *ScriptFlowHandler) HandleBatchScriptGenerateJob(ctx context.Context, job *models.Job, tools *jobservice.JobTools) (map[string]any, error) {
	h.log.Info("handling script.generate_batch job", zap.String("job_id", job.ID))
	var req GenerateBatchRequest
	if err := json.Unmarshal(job.Payload, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal job payload: %w", err)
	}

	// Make sure Async is false inside execution to prevent re-enqueueing
	req.Async = false

	var progressFunc func(int, string)
	if tools != nil && tools.Progress != nil {
		progressFunc = func(pct int, msg string) {
			tools.Progress(pct, msg)
		}
	}

	resp, err := h.ExecuteBatchGeneration(ctx, &req, progressFunc)
	if err != nil {
		return nil, err
	}
	// Convert typed response to map for the job system.
	return resp.ToMap(), nil
}
