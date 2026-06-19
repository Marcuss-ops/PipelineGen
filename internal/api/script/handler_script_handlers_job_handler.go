package script

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scriptflow/batch"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"

	"go.uber.org/zap"
)

// RegisterJobHandlers registers the handlers for script jobs
func (h *ScriptFlowHandler) RegisterJobHandlers(jobsSvc *jobservice.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(jobservice.JobTypeBatchScriptGenerate, h.HandleBatchScriptGenerateJob)
		h.log.Info("registered script.generate_batch job handler")
		jobsSvc.RegisterHandler(jobservice.JobTypeClipScriptGenerate, h.HandleClipScriptGenerateJob)
		h.log.Info("registered script.generate_from_clips job handler")
		if h.catalogJobService != nil {
			jobsSvc.RegisterHandler(jobservice.JobTypeCatalogScriptGenerate, h.catalogJobService.HandleCatalogScriptGenerateJob)
			h.log.Info("registered script.generate_from_catalog job handler")
		}
		if h.curationJobService != nil {
			jobsSvc.RegisterHandler("script.curate", h.curationJobService.HandleCurateJob)
			h.log.Info("registered script.curate job handler")
		}
	}
}

// HandleBatchScriptGenerateJob processes the background job for script.generate_batch
func (h *ScriptFlowHandler) HandleBatchScriptGenerateJob(ctx context.Context, job *jobservice.Job, tools *jobservice.JobTools) (map[string]any, error) {
	h.log.Info("handling script.generate_batch job", zap.String("job_id", job.ID))
	var req batch.GenerateBatchRequest
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

	if h.batchService == nil {
		return nil, fmt.Errorf("batch service not initialized")
	}
	resp, err := h.batchService.Execute(ctx, &req, progressFunc)
	if err != nil {
		return nil, err
	}
	// Convert typed response to map for the job system.
	return resp.ToMap(), nil
}
