package overlays

import (
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// buildPlanForTest keeps planner unit tests focused on map composition and
// admission; Rust owns map timing in production.
func buildPlanForTest(input PlanInput, config PlannerConfig) (capmodel.OverlayPlan, error) {
	return BuildPlan(input, config)
}
