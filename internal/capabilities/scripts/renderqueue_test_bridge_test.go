package scriptgeneration

import (
	"context"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/renderqueue"
)

// renderDynamicMapVideoOnce is the root test-facing bridge to the phase-owned
// renderer. Production code goes through the enqueuer, not this helper.
func renderDynamicMapVideoOnce(ctx context.Context, plan capmodel.OverlayPlan, gate DynamicMapGPUAdmission, cacheDirs ...string) (string, error) {
	return renderqueue.RenderDynamicMapVideoOnce(ctx, plan, gate, cacheDirs...)
}
