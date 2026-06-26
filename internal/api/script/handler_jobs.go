// Package script (api/script) — handler_jobs.go carries the
// job-system handler receiver methods for ScriptFlowHandler.
//
// PR-A (June 2026): HandleBatchScriptGenerateJob was moved to
// internal/application/scripts/batch_job.go (BatchJobHandler) as
// part of Wave 19's single-source-of-truth cleanup. The worker's
// canonical owner is now internal/application/scripts, not
// internal/api/script. architecture/ownership.yaml::job_handler_map
// reflects the move.
//
// Job handler registration moved to wire_script.go.
// HandleClipScriptGenerateJob delegates to PipelineUseCase
// (already canonical; untouched by PR-A).
package script

import (
	"context"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// HandleClipScriptGenerateJob delegates to PipelineUseCase.
// Canonical: PipelineUseCase.HandleJob is the SSOT for the
// `script.generate_from_clips` job type.
func (h *ScriptFlowHandler) HandleClipScriptGenerateJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	if h.pipelineUC == nil {
		return nil, fmt.Errorf("script flow pipeline use case not initialized")
	}
	return h.pipelineUC.HandleJob(ctx, j, tools)
}
