package jobs

import (
	"errors"
	"fmt"

	domjob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func validateFinalizeAttemptCommand(cmd domjob.FinalizeAttemptCommand) error {
	if cmd.JobID == "" {
		return errors.New("FinalizeAttempt: JobID required")
	}
	if !cmd.Outcome.IsValid() {
		return fmt.Errorf("%w: %q", ErrFinalizeAttemptOutcomeInvalid, cmd.Outcome)
	}
	if cmd.Outcome == domjob.OutcomeSucceeded && len(cmd.Result) == 0 {
		return ErrFinalizeAttemptResultMissing
	}
	if cmd.Outcome != domjob.OutcomeSucceeded && cmd.ErrorMessage == "" {
		return ErrFinalizeAttemptErrorMissing
	}
	if len(cmd.DLQPayload) > 0 && cmd.Outcome == domjob.OutcomeSucceeded {
		return ErrFinalizeAttemptDLQIncompatible
	}
	for _, event := range cmd.OutboxEvents {
		if event.Type == "" || event.EventKey == "" {
			return ErrFinalizeAttemptOutboxEventMissing
		}
	}
	return nil
}

type finalizeAttemptDecision struct {
	targetStatus   domjob.Status
	incrementRetry bool
	errorMessage   string
}

func decideFinalizeAttempt(outcome domjob.FinalizeAttemptOutcome, errorMessage string, retryCount, maxRetries int) (finalizeAttemptDecision, error) {
	decision := finalizeAttemptDecision{targetStatus: domjob.StatusSucceeded, errorMessage: errorMessage}
	switch outcome {
	case domjob.OutcomeSucceeded:
		decision.targetStatus = domjob.StatusSucceeded
	case domjob.OutcomeFailedPermanent:
		decision.targetStatus = domjob.StatusFailed
	case domjob.OutcomeScheduleRetry:
		if retryCount+1 > maxRetries {
			decision.targetStatus = domjob.StatusFailed
			decision.errorMessage = errorMessage + " (max retries exhausted)"
		} else {
			decision.targetStatus = domjob.StatusRetryWait
			decision.incrementRetry = true
		}
	default:
		return finalizeAttemptDecision{}, fmt.Errorf("%w: %q", ErrFinalizeAttemptOutcomeInvalid, outcome)
	}
	return decision, nil
}
