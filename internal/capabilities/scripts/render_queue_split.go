package scriptgeneration

// render_queue_items.go owns the ONLY local overlay render entry point.
//
// PipelineGen renders independent overlay item clips. The whole-timeline
// canvas submission and the per-scene split were removed: a local run must
// never encode a full timeline or a scene-wide composition. A final
// compositor, when one is requested, consumes the certified per-item
// artifacts and their batch/manifest reference.

import (
	"context"
	"fmt"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// OverlayPlanItemEnqueuer renders every semantic overlay as its own short
// alpha-bearing artifact. It is the only overlay render capability the local
// runner accepts.
type OverlayPlanItemEnqueuer interface {
	EnqueueChrononPlanItems(context.Context, capmodel.OverlayPlan) (RenderReference, error)
}

// EnqueueChrononPlanItems emits one short alpha-bearing artifact per semantic
// overlay. No scene-wide or whole-timeline canvas is made.
func (e *QueueRenderEnqueuer) EnqueueChrononPlanItems(ctx context.Context, plan capmodel.OverlayPlan) (RenderReference, error) {
	candidates := overlayRenderableCandidates(plan)
	if len(candidates) == 0 {
		return RenderReference{}, fmt.Errorf("overlay plan %q has no renderable items", plan.PlanID)
	}
	return e.enqueueStandaloneOverlayItems(ctx, plan, candidates)
}
