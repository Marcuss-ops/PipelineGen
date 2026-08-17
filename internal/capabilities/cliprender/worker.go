package cliprender

import (
	"context"
	"errors"
	"fmt"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ErrRenderWorkerNotImplemented is the typed sentinel returned by the
// scaffold handler binding. The canonical render worker (ClipRenderPlanV1
// compilation, parallel materialization, transcript/subtitle resolution,
// single Rust render pass, contract validation, Drive upload, derived
// asset commit) replaces this binding in the follow-up step.
var ErrRenderWorkerNotImplemented = errors.New("clip.render worker not implemented yet (scaffold binding — the canonical render worker lands in the follow-up step)")

// NotImplementedHandler is the TEMPORARY job-handler binding for
// clip.render.
//
// It exists only so the enqueue fail-closed gate (jobs.Service rejects
// enqueues for job types without a registered handler — no silent
// queue buildup) accepts clip.render jobs while the canonical worker is
// built. Claimed jobs fail loudly with the typed sentinel instead of
// hanging in the queue. The composition root swaps this binding for the
// real worker handler in the follow-up step.
func NotImplementedHandler(_ context.Context, j *job.Job, _ *job.JobExecutionTools) (job.Result, error) {
	return nil, fmt.Errorf("%w: job_id=%s", ErrRenderWorkerNotImplemented, j.ID)
}
