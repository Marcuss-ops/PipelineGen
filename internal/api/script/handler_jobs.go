// Package script (api/script) — handler_jobs.go carries the
// job-system handler receiver methods for ScriptFlowHandler plus
// back-compat type aliases for CatalogJobServiceImpl and
// CurationJobServiceImpl.
//
// Wave 14 problem #4 (June 2026): the heavy orchestration that
// previously lived in ScriptFlowHandler.HandleClipScriptGenerateJob
// (semaphore, payload decode, 3-path switch, prewarm goroutine,
// pipeline invocation, buildFinalResult) now lives in
// internal/application/scripts/pipeline_usecase.go etc. This file
// contains ONLY:
//   - a thin HandleClipScriptGenerateJob that delegates to
//     h.pipelineUC.HandleJob
//   - HandleBatchScriptGenerateJob (unchanged — already thin)
//   - a thin RegisterJobHandlers that delegates registration to the
//     two use cases (pipeline UC + batch UC)
//   - back-compat CatalogJobService / CurationJobService type aliases
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

// ── Job registration ────────────────────────────────────────────────────────

// RegisterJobHandlers is the canonical entry point for the job
// system. It delegates registration of every script-flow job type
// to the application-layer use cases so the handler no longer owns
// the registration logic (handler is pure transport).
func (h *ScriptFlowHandler) RegisterJobHandlers(jobsSvc *job.Service) {
	if jobsSvc == nil {
		return
	}
	if h.batchService != nil {
		jobsSvc.RegisterHandler(job.TypeBatchScriptGenerate, h.HandleBatchScriptGenerateJob)
		if h.log != nil {
			h.log.Info("registered script.generate_batch job handler")
		}
	}
	if h.catalogJobService != nil {
		jobsSvc.RegisterHandler(job.TypeCatalogScriptGenerate, h.catalogJobService.HandleCatalogScriptGenerateJob)
		if h.log != nil {
			h.log.Info("registered script.generate_from_catalog job handler")
		}
	}
	if h.curationJobService != nil {
		jobsSvc.RegisterHandler("script.curate", h.curationJobService.HandleCurateJob)
		if h.log != nil {
			h.log.Info("registered script.curate job handler")
		}
	}
	// PipelineUseCase owns unified script generation.
	if h.pipelineUC != nil {
		if err := h.pipelineUC.RegisterJobs(jobsSvc); err != nil {
			if h.log != nil {
				h.log.Warn("pipeline use case job registration failed", zap.Error(err))
			}
		}
	}
}

// HandleBatchScriptGenerateJob is the existing thin handler for the
// batch-generation job type. Payload unmarshal + delegate to
// BatchService.Execute. Already aligned with the new pattern.
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

// HandleClipScriptGenerateJob is the THIN router. All orchestration
// (semaphore, prewarm, payload decode, 3-path dispatch, pipeline
// invocation, result shaping) lives in PipelineUseCase — see
// internal/application/scripts/pipeline_usecase.go. This method
// only exists because the job system requires a function reference
// registered into the job dispatcher; the function body delegates
// to the use case.
func (h *ScriptFlowHandler) HandleClipScriptGenerateJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	if h.pipelineUC == nil {
		return nil, fmt.Errorf("script flow pipeline use case not initialized")
	}
	return h.pipelineUC.HandleJob(ctx, j, tools)
}

// ── Inline catalog/curation job service thin wrappers (PR2 back-compat) ─────

// CatalogJobServiceImpl delegates to the application-layer implementation.
type CatalogJobServiceImpl = scripts.CatalogJobServiceImpl

// NewCatalogJobServiceImpl creates the catalog job service.
var NewCatalogJobServiceImpl = scripts.NewCatalogJobServiceImpl

// CurationJobServiceImpl delegates to the application-layer implementation.
type CurationJobServiceImpl = scripts.CurationJobServiceImpl

// NewCurationJobServiceImpl creates the curation job service.
var NewCurationJobServiceImpl = scripts.NewCurationJobServiceImpl

// ── CatalogJobService + CurationJobService compile-time assertions ──────────

var _ CatalogJobService = (*CatalogJobServiceImpl)(nil)
var _ CurationJobService = (*CurationJobServiceImpl)(nil)
