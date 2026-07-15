// Package job preserves compatibility aliases for the canonical kernel/job
// error contracts. New production code must import internal/kernel/job.
package job

import kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

var (
	ErrLeaseLost          = kerneljob.ErrLeaseLost
	ErrTransitionConflict = kerneljob.ErrTransitionConflict
	ErrJobNotFound        = kerneljob.ErrJobNotFound

	ErrFinalizeAttemptOutcomeInvalid    = kerneljob.ErrFinalizeAttemptOutcomeInvalid
	ErrFinalizeAttemptResultMissing     = kerneljob.ErrFinalizeAttemptResultMissing
	ErrFinalizeAttemptErrorMissing      = kerneljob.ErrFinalizeAttemptErrorMissing
	ErrFinalizeAttemptArtifactStale     = kerneljob.ErrFinalizeAttemptArtifactStale
	ErrFinalizeAttemptOutboxEventMissing = kerneljob.ErrFinalizeAttemptOutboxEventMissing
	ErrFinalizeAttemptDLQIncompatible   = kerneljob.ErrFinalizeAttemptDLQIncompatible
)
