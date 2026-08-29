package jobs

import job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

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
