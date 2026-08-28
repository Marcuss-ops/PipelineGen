package jobs

import (
	"encoding/json"
	"errors"
	"testing"

	domjob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func TestValidateFinalizeAttemptCommandRejectsInvalidInputs(t *testing.T) {
	base := domjob.FinalizeAttemptCommand{JobID: "job", Outcome: domjob.OutcomeSucceeded, Result: json.RawMessage(`{"ok":true}`)}
	cases := []struct {
		name string
		cmd  domjob.FinalizeAttemptCommand
		want error
	}{
		{"invalid outcome", domjob.FinalizeAttemptCommand{JobID: "job", Outcome: domjob.FinalizeAttemptOutcome("bad")}, ErrFinalizeAttemptOutcomeInvalid},
		{"missing result", domjob.FinalizeAttemptCommand{JobID: "job", Outcome: domjob.OutcomeSucceeded}, ErrFinalizeAttemptResultMissing},
		{"missing error", domjob.FinalizeAttemptCommand{JobID: "job", Outcome: domjob.OutcomeFailedPermanent}, ErrFinalizeAttemptErrorMissing},
		{"incompatible dlq", func() domjob.FinalizeAttemptCommand { cmd := base; cmd.DLQPayload = json.RawMessage(`{}`); return cmd }(), ErrFinalizeAttemptDLQIncompatible},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateFinalizeAttemptCommand(tc.cmd); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDecideFinalizeAttemptRetryPolicy(t *testing.T) {
	under, err := decideFinalizeAttempt(domjob.OutcomeScheduleRetry, "temporary", 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if under.targetStatus != domjob.StatusRetryWait || !under.incrementRetry || under.errorMessage != "temporary" {
		t.Fatalf("under-limit decision = %+v", under)
	}

	exhausted, err := decideFinalizeAttempt(domjob.OutcomeScheduleRetry, "temporary", 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	if exhausted.targetStatus != domjob.StatusFailed || exhausted.incrementRetry || exhausted.errorMessage != "temporary (max retries exhausted)" {
		t.Fatalf("exhausted decision = %+v", exhausted)
	}
}
