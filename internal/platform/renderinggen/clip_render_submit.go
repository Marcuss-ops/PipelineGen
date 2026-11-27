package renderinggen

import (
	"context"
	"errors"
	"fmt"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	renderqueue "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/renderqueue"
	"github.com/Marcuss-ops/PipelineGen/pkg/corid"
	queueclient "github.com/Marcuss-ops/RenderingGen/queue/client"
)

// Submit is the PRE-RENDER half of the boundary (Wave B): validate the plan,
// map it onto the renderinggen.overlay-plan.v1 contract, resolve + prefetch its
// content-addressed assets and enqueue the remote render. It returns as soon as
// the remote job is ACCEPTED — it never waits for the render, which is what
// lets the caller release its worker slot.
//
// Idempotent by construction: the remote job id is plan.RunID, so a retried
// submit addresses the same remote job (the queue answers 200 only after atomic identity verification)
// and a FAILED remote job is reset to pending rather than left stuck.
func (e *ClipRenderExecutor) Submit(ctx context.Context, plan cliprender.ClipRenderPlanV1) error {
	if e == nil || e.queue == nil {
		return fmt.Errorf("%w: RenderingGen queue is not configured", cliprender.ErrBackendUnavailable)
	}
	if err := plan.Validate(); err != nil {
		return fmt.Errorf("renderinggen clip executor: validate plan: %w", err)
	}
	if err := e.requireGPUInventory(ctx); err != nil {
		return fmt.Errorf("renderinggen clip executor: %w", err)
	}
	if requested, alignment, ok := e.chunkPolicy(plan); ok {
		if _, err := e.SubmitChunked(ctx, plan, requested, alignment); err != nil {
			return fmt.Errorf("renderinggen clip executor: submit chunk family: %w", err)
		}
		return nil
	}
	// MapClipPlanToOverlayPlan produces the renderinggen.overlay-plan.v1
	// semantic contract. Sending a raw ClipRenderPlanV1 (no schema_version)
	// would cause the RenderingGen worker to treat it as a Chronon concrete
	// plan pass-through — a silent data corruption. This function is the
	// single authoritative serialisation point for clip render jobs.
	rawPlan, err := MapClipPlanToOverlayPlan(plan)
	if err != nil {
		return fmt.Errorf("renderinggen clip executor: map plan: %w", err)
	}
	// Build hash-addressed asset refs. LogicalPath uses the content-addressed
	// object-store key so remote RenderingGen workers can materialise each
	// asset by hash — local VPS paths are never forwarded to the queue.
	refs, err := overlayPlanAssets(plan)
	if err != nil {
		return fmt.Errorf("renderinggen clip executor: asset refs: %w", err)
	}
	if err := prefetchClipAssets(ctx, plan, refs); err != nil {
		return fmt.Errorf("renderinggen clip executor: prefetch assets: %w", err)
	}
	assets := make([]queueclient.AssetRef, len(refs))
	for i, r := range refs {
		assets[i] = queueclient.AssetRef{Hash: r.Hash, LogicalPath: r.LogicalPath}
	}
	// ParentJobID is the correlation id of the run that produced this plan
	// (pkg/corid). The remote queue stores it with the job, so a slow or
	// failed render can be traced back to the master run and its logs
	// WITHOUT sharing a process: the plan revision alone only joins the
	// render artifact, not the run that asked for it.
	submitErr := e.queue.Submit(ctx, renderqueue.RenderQueueJob{
		ID:          plan.RunID,
		JobType:     "render_segment",
		ParentJobID: corid.FromContext(ctx),
		OverlaySpec: rawPlan,
		Assets:      scriptAssets(assets),
		// Chronon Vulkan is the only permitted backend. The tri-state field
		// must be TRUE: nil derives gpu_preferred, which lets a worker without
		// the GPU capability claim the job and only fail at completion.
		RequireGPU: boolPtr(true),
	})
	if submitErr != nil && errors.Is(submitErr, renderqueue.ErrRenderRequestConflict) {
		// The queue's immutable request includes parent_job_id (the producing
		// run's correlation id), so the SAME content-addressed plan submitted
		// by a new run collides even though it asks for identical bytes.
		// Recover only when the existing job carries an equivalent plan; a
		// genuinely different plan under the same id stays a fail-closed
		// conflict. The job id is the sealed plan revision, so an equivalent
		// plan means this render, not a different one wearing its id.
		existing, getErr := e.queue.Get(ctx, plan.RunID)
		if getErr == nil && jsonEquivalent(existing.OverlaySpec, rawPlan) {
			if existing.State == renderqueue.RenderQueueStateFailed {
				if rearmErr := renderqueue.RearmFailedRenderJob(ctx, e.queue, plan.RunID); rearmErr != nil {
					return fmt.Errorf("renderinggen clip executor: submit: %w", rearmErr)
				}
			}
			submitErr = nil
		}
	}
	if submitErr != nil && !errors.Is(submitErr, renderqueue.ErrJobExists) {
		return fmt.Errorf("renderinggen clip executor: submit: %w", submitErr)
	}
	if submitErr != nil && errors.Is(submitErr, renderqueue.ErrJobExists) {
		// A failed re-run of the same clip must not leave the job stuck in
		// FAILED while the caller believes a render is underway. The rule is
		// owned by the capability (renderqueue.RearmFailedRenderJob), so the
		// overlay enqueuer and this executor cannot answer it differently.
		if rearmErr := renderqueue.RearmFailedRenderJob(ctx, e.queue, plan.RunID); rearmErr != nil {
			return fmt.Errorf("renderinggen clip executor: %w", rearmErr)
		}
	}
	return nil
}
