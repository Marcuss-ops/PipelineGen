package jobs

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/scheduling"
)

// Type aliases to the canonical lower layer.
type (
	PreparationPlanner     = scheduling.PreparationPlanner
	PreparationPlan        = scheduling.PreparationPlan
	PreparationUnit        = scheduling.PreparationUnit
	JobPreparationRegistry = scheduling.JobPreparationRegistry
)

// NewJobPreparationRegistry resolves a planner by canonical job type.
func NewJobPreparationRegistry() *scheduling.JobPreparationRegistry {
	return scheduling.NewJobPreparationRegistry()
}

// RegisterPreparationPlanner is the explicit composition-root registration
// seam.
func RegisterPreparationPlanner(registry *scheduling.JobPreparationRegistry, jobType string, planner scheduling.PreparationPlanner) error {
	return scheduling.RegisterPreparationPlanner(registry, jobType, planner)
}
