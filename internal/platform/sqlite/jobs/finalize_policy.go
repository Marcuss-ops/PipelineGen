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
	// A deferral MUST state the instant it may come back: the requeue sweep
	// honours jobs.deferred_until, so a deferral without a delay would park the
	// row in RETRY_WAIT forever (godlike/07 fail-closed). The worker fills the
	// deployment default before calling the store.
	if cmd.Outcome == domjob.OutcomeDeferred && cmd.Backoff <= 0 {
		return ErrFinalizeAttemptDeferralDelayMissing
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
	targetStatus domjob.Status
	// incrementRetry is true only for a retry that SPENDS the budget. A
	// deferral is a wait, so it leaves retry_count alone (see OutcomeDeferred).
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
	case domjob.OutcomeDeferred:
		// A deferral is a WAIT, not a failure: it lands in the same
		// non-terminal waiting state a retry uses (so cancellation, aggregation
		// and the RETRY_WAIT operator views keep working unchanged) but it does
		// NOT spend the retry budget, and it is never downgraded to FAILED —
		// there is no budget to exhaust. The row's DeferredUntil column carries
		// when it may be re-dispatched; the requeue sweep honours it.
		decision.targetStatus = domjob.StatusRetryWait
	default:
		return finalizeAttemptDecision{}, fmt.Errorf("%w: %q", ErrFinalizeAttemptOutcomeInvalid, outcome)
	}
	return decision, nil
}
