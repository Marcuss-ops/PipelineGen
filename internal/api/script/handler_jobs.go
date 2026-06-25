// Package script (api/script) — handler_jobs.go carries the
// job-system handler receiver methods for ScriptFlowHandler.
//
// Job handler registration moved to wire_script.go.
// HandleClipScriptGenerateJob delegates to PipelineUseCase.
// HandleBatchScriptGenerateJob is a thin decoder+delegate.
package script

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// HandleBatchScriptGenerateJob is the thin handler for the batch-generation
// job type. Payload unmarshal + delegate to BatchService.Execute.
func (h *ScriptFlowHandler) HandleBatchScriptGenerateJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	if h.log != nil {
		h.log.Info("handling script.generate_batch job", zap.String("job_id", j.ID))
	}
	var req scripts.GenerateBatchRequest
	if err := json.Unmarshal(j.Payload, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal job payload: %w", err)
	}
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
	return resp.ToMap(), nil
}

// HandleClipScriptGenerateJob delegates to PipelineUseCase.
func (h *ScriptFlowHandler) HandleClipScriptGenerateJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	if h.pipelineUC == nil {
		return nil, fmt.Errorf("script flow pipeline use case not initialized")
	}
	return h.pipelineUC.HandleJob(ctx, j, tools)
}
