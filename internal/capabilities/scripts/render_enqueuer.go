package scriptgeneration

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
)

// RenderPlanExecutor is the narrow port implemented by the concrete Velox
// media adapter. Keeping it here lets the generation runtime remain unaware of
// infrastructure packages while still enforcing the canonical plan boundary.
type RenderPlanExecutor interface {
	RenderCanonicalPlan(context.Context, render.ValidatedRenderPlan) error
}

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
