// Package jobs — preparation.go: the preparation-domain type aliases and
// the composition-root registry seam (merged from preparation_store.go and
// preparation_registry.go so the package stays at its registered file-count
// baseline).
package jobs

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/scheduling"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

type PreparationState = job.PreparationState
type PreparedUnit = job.PreparedUnit
type PreparationStore = job.PreparationStore
type PreparationUnitClaim = job.PreparationUnitClaim
type PreparationReadyUpdate = job.PreparationReadyUpdate
type PreparationPlanInput = job.PreparationPlanInput
type PreparationJobUnit = job.PreparationJobUnit
type RegisterPreparationJobUnitInput = job.RegisterPreparationJobUnitInput

const (
	PreparationPlanned = job.PreparationPlanned
	PreparationRunning = job.PreparationRunning
	PreparationReady   = job.PreparationReady
	PreparationFailed  = job.PreparationFailed
	PreparationStale   = job.PreparationStale
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
