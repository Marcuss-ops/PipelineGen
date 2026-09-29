package jobs

// Live regression, promoted from production: a script.generate job whose
// durable run failed with its OWN retry scheduled used to be failed and
// dead-lettered by the job plane — 132 ms after the run wrote
// `next_retry_at`, and while the remote render it was waiting on was still in
// flight. The run's schedule had no driver (ShouldRetry has no production
// caller: the job attempt IS the driver), so the retry was orphaned and the job
// died with work still running.
//
// The contract these tests pin: a failed run that still has budget is a WAIT,
// so the handler hands the JOB's attempt back as a deferral (which spends none
// of the job's retry budget) and the next attempt resumes the run. Once the run
// has no retry left, the failure is terminal for both.
//
// `pendingRunRetry` / `incompleteRunError` are the single decision point, so
// these tests exercise the real translation the handler performs — not a copy.

import (
	"errors"
	"strings"
	"testing"
	"time"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func TestPendingRunRetry_OnlyAWaitWithBudgetQualifies(t *testing.T) {
	future := time.Now().UTC().Add(12 * time.Second)
	past := time.Now().UTC().Add(-2 * time.Second)

	cases := []struct {
		name  string
		run   *scriptgen.GenerationRun
		want  bool
		delay time.Duration // only checked when want is true
	}{
		{
			name:  "failed run with a future retry waits",
			run:   &scriptgen.GenerationRun{ID: "r1", Status: scriptgen.RunStatusFailed, AttemptCount: 1, NextRetryAt: &future},
			want:  true,
			delay: time.Until(future),
		},
		{
			name: "a due schedule still defers (the worker fills the default delay)",
			run:  &scriptgen.GenerationRun{ID: "r2", Status: scriptgen.RunStatusFailed, AttemptCount: 1, NextRetryAt: &past},
			want: true,
		},
		{
			name: "budget exhausted is terminal, not a wait",
			run:  &scriptgen.GenerationRun{ID: "r3", Status: scriptgen.RunStatusFailed, AttemptCount: scriptgen.MaxRetries, NextRetryAt: &future},
			want: false,
		},
		{
			name: "no schedule written means the run decided it is over",
			run:  &scriptgen.GenerationRun{ID: "r4", Status: scriptgen.RunStatusFailed, AttemptCount: 1},
			want: false,
		},
		{
			name: "a completed run is never a wait",
			run:  &scriptgen.GenerationRun{ID: "r5", Status: scriptgen.RunStatusCompleted, AttemptCount: 0, NextRetryAt: &future},
			want: false,
		},
		{
			name: "a running run is never a wait here",
			run:  &scriptgen.GenerationRun{ID: "r6", Status: scriptgen.RunStatusRunning, AttemptCount: 0, NextRetryAt: &future},
			want: false,
		},
		{
			name: "nil run cannot be interpreted",
			run:  nil,
			want: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			delay, got := pendingRunRetry(tc.run)
			if got != tc.want {
				t.Fatalf("pendingRunRetry = %v, want %v", got, tc.want)
			}
			if !got {
				if delay != 0 {
					t.Fatalf("a non-wait must not carry a delay, got %s", delay)
				}
				return
			}
			if delay < 0 {
				t.Fatalf("a wait must never be negative, got %s", delay)
			}
			if tc.delay != 0 && (delay > tc.delay+time.Second || delay < tc.delay-time.Second) {
				t.Fatalf("delay = %s, want ~%s", delay, tc.delay)
			}
		})
	}
}

// TestIncompleteRunError_TranslatesAWaitIntoAJobDeferral is the fix itself:
// the returned error must be recognized by the worker's deferral gate
// (kernel/job.AsDeferral) and carry the run's cadence, so the job goes to
// RETRY_WAIT with retry_count untouched instead of FAILED + dead-letter.
func TestIncompleteRunError_TranslatesAWaitIntoAJobDeferral(t *testing.T) {
	retryAt := time.Now().UTC().Add(30 * time.Second)
	run := &scriptgen.GenerationRun{
		ID:           "run_waiting",
		Status:       scriptgen.RunStatusFailed,
		FailedStage:  scriptgen.StagePublishingDocuments,
		AttemptCount: 1,
		NextRetryAt:  &retryAt,
		ErrorMessage: "remote final job job_ed7d is still PENDING: remote final job is still rendering",
	}

	err := incompleteRunError(run)
	if err == nil {
		t.Fatal("a failed run must produce an error")
	}
	deferral, ok := job.AsDeferral(err)
	if !ok {
		t.Fatalf("a run with a retry scheduled must defer the job, got %v", err)
	}
	if deferral.Delay <= 0 || deferral.Delay > 31*time.Second {
		t.Fatalf("deferral delay = %s, want the wait until next_retry_at (~30s)", deferral.Delay)
	}
	for _, want := range []string{"run_waiting", "PUBLISHING_DOCUMENTS", "run attempt 1/3", "still PENDING"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("deferral message %q must name %q", err.Error(), want)
		}
	}
	// A deferral is not a retryable failure: nothing may classify it as one.
	if errors.Is(err, scriptgen.ErrFinalJobPending) {
		t.Fatal("the deferral must not be confused with the transport sentinel")
	}
}

func TestIncompleteRunError_ExhaustedRunIsATerminalFailure(t *testing.T) {
	run := &scriptgen.GenerationRun{
		ID:           "run_dead",
		Status:       scriptgen.RunStatusFailed,
		FailedStage:  scriptgen.StagePublishingDocuments,
		AttemptCount: scriptgen.MaxRetries,
		ErrorMessage: "remote final job job_dead is still RUNNING: remote final job is still rendering",
	}

	err := incompleteRunError(run)
	if err == nil {
		t.Fatal("an exhausted run must fail the job")
	}
	if _, ok := job.AsDeferral(err); ok {
		t.Fatalf("a run with no retry left must NOT defer the job, got %v", err)
	}
	if !strings.Contains(err.Error(), "durable run failed") {
		t.Fatalf("terminal message must stay the canonical one, got %q", err.Error())
	}
}

func TestIncompleteRunError_NilRunStaysTerminal(t *testing.T) {
	err := incompleteRunError(nil)
	if err == nil {
		t.Fatal("a missing run must fail the job")
	}
	if _, ok := job.AsDeferral(err); ok {
		t.Fatalf("a missing run must not defer, got %v", err)
	}
}
