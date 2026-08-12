package scriptgeneration

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	videojob "github.com/Marcuss-ops/PipelineGen/internal/domain/video"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// RenderPlanExecutor is the narrow port implemented by the concrete Velox
// media adapter. Keeping it here lets the generation runtime remain unaware of
// infrastructure packages while still enforcing the canonical plan boundary.
type RenderPlanExecutor interface {
	RenderCanonicalPlan(context.Context, render.ValidatedRenderPlan) error
}

// JobRenderEnqueuer is the production boundary for render work. It creates a
// canonical render.video job; the worker handler owns the actual executor.
type JobRenderEnqueuer struct{ service job.Service }

func NewJobRenderEnqueuer(service job.Service) (*JobRenderEnqueuer, error) {
	if service == nil {
		return nil, fmt.Errorf("job render enqueuer requires job service")
	}
	return &JobRenderEnqueuer{service: service}, nil
}

func (e *JobRenderEnqueuer) Enqueue(ctx context.Context, result GenerateResult) (RenderReference, error) {
	if e == nil || e.service == nil {
		return RenderReference{}, fmt.Errorf("job render enqueuer is not configured")
	}
	if result.RenderPlan == nil {
		return RenderReference{}, fmt.Errorf("job render enqueue requires RenderPlan")
	}
	if _, err := render.ValidateRenderPlan(*result.RenderPlan); err != nil {
		return RenderReference{}, fmt.Errorf("job render enqueue validation failed: %w", err)
	}
	j, err := e.service.Enqueue(ctx, &job.EnqueueRequest{Type: videojob.TypeRender, Payload: result.RenderPlan, ActiveKey: result.RenderPlan.JobID})
	if err != nil {
		return RenderReference{}, fmt.Errorf("enqueue render.video: %w", err)
	}
	return RenderReference{JobID: j.ID, Status: string(j.Status)}, nil
}

var _ RenderEnqueuer = (*JobRenderEnqueuer)(nil)

// CanonicalRenderEnqueuer adapts a RenderPlanExecutor to the generation
// RenderEnqueuer port. A queue-backed deployment may provide its own
// RenderEnqueuer; this adapter is for the local executor topology.
type CanonicalRenderEnqueuer struct {
	executor RenderPlanExecutor
}

func NewCanonicalRenderEnqueuer(executor RenderPlanExecutor) (*CanonicalRenderEnqueuer, error) {
	if executor == nil {
		return nil, fmt.Errorf("canonical render enqueuer requires an executor")
	}
	return &CanonicalRenderEnqueuer{executor: executor}, nil
}

func (e *CanonicalRenderEnqueuer) Enqueue(ctx context.Context, result GenerateResult) (RenderReference, error) {
	if e == nil || e.executor == nil {
		return RenderReference{}, fmt.Errorf("canonical render enqueuer is not configured")
	}
	if result.RenderPlan == nil {
		return RenderReference{}, fmt.Errorf("canonical render enqueue requires RenderPlan")
	}
	validated, err := render.ValidateRenderPlan(*result.RenderPlan)
	if err != nil {
		return RenderReference{}, fmt.Errorf("canonical render enqueue validation failed: %w", err)
	}
	if err := e.executor.RenderCanonicalPlan(ctx, validated); err != nil {
		return RenderReference{}, fmt.Errorf("canonical render executor failed: %w", err)
	}
	return RenderReference{JobID: result.RenderPlan.JobID, Status: "COMPLETED"}, nil
}

var _ RenderEnqueuer = (*CanonicalRenderEnqueuer)(nil)
