// worker_dispatch_retry_gate_test.go — pins the worker's retry-vs-terminal
// gate for a FAILED dispatch.
//
// The gate is `retry.IsTransient(dispatchErr) && DecideRetry(j) ==
// RetryScheduled` (worker_finalize_paths.go). retry.IsTransient is a PURE
// TYPED PROBE since FASE 6 Cut 6.1.D: an error is retryable only if it
// satisfies pkg/retry.RetryableError (IsRetryable() bool) or wraps a
// *retry.TransientInfrastructureError. That is exactly why a plain
// `errors.New("...retryable failure")` sentinel used to dead-letter a
// transient YouTube extraction on the first attempt.
//
// The YouTube side of the contract (its errors satisfy the typed probe) is
// pinned in internal/capabilities/youtube/jobs (retry_broker_wiring_test.go):
// that package imports this one, so the composition cannot be exercised
// from here without an import cycle. These two pins together cover the
// chain end to end:
//
//	youtube handler error --[IsRetryable]--> retry.IsTransient == true
//	                                             |
//	                       retry.IsTransient == true --[this file]--> ScheduleRetry
package jobs

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// recordingDispatchStore records which terminal write the worker chose for
// a failed dispatch. The embedded job.Store is intentionally nil: any other
// broker call panics loudly instead of silently passing the test.
type recordingDispatchStore struct {
	job.Store
	scheduled   int
	failed      int
	deadLetters int
}

func (r *recordingDispatchStore) ScheduleRetry(_ context.Context, _ string, _, _ string, _ int, _ string, _ time.Duration) error {
	r.scheduled++
	return nil
}

func (r *recordingDispatchStore) Fail(_ context.Context, _ string, _, _ string, _ int, _ string) error {
	r.failed++
	return nil
}

func (r *recordingDispatchStore) DeadLetter(_ context.Context, _ string, _ string) error {
	r.deadLetters++
	return nil
}

// typedRetryableDispatchError mirrors the structural retryability contract
// that the YouTube extraction errors satisfy by construction
// (usecase.*ExtractionError.IsRetryable, jobs.ErrExtractionRetryable,
// *jobs.PartialSuccessError).
type typedRetryableDispatchError struct{ msg string }

func (e typedRetryableDispatchError) Error() string { return e.msg }

func (e typedRetryableDispatchError) IsRetryable() bool { return true }

func newDispatchGateWorker(rec *recordingDispatchStore) *Worker {
	return &Worker{id: "retry-gate-worker", repo: rec, log: zap.NewNop()}
}

// TestFinalizeJobDispatchError_TypedRetryableSchedulesRetry is the
// regression test for the dead-letter-on-first-attempt bug: a transient
// verdict must consume the retry budget instead of failing the job.
func TestFinalizeJobDispatchError_TypedRetryableSchedulesRetry(t *testing.T) {
	rec := &recordingDispatchStore{}
	w := newDispatchGateWorker(rec)
	j := &job.Job{ID: "job-1", Type: "youtube_clip.extract", RetryCount: 0, MaxRetries: 2}

	w.finalizeJobDispatchError(context.Background(), j, w.id, "lease-1", 1,
		typedRetryableDispatchError{msg: "youtube extraction: retryable failure"})

	require.Equal(t, 1, rec.scheduled, "a typed retryable dispatch error MUST be scheduled for retry")
	require.Zero(t, rec.failed)
	require.Zero(t, rec.deadLetters)
}

// TestFinalizeJobDispatchError_RetryableSurvivesHandlerEnvelope pins that
// the classification envelope the YouTube handler builds
// (fmt.Errorf("extraction classified: %w — failure_details=…")) does not
// hide the typed verdict from the gate.
func TestFinalizeJobDispatchError_RetryableSurvivesHandlerEnvelope(t *testing.T) {
	rec := &recordingDispatchStore{}
	w := newDispatchGateWorker(rec)
	j := &job.Job{ID: "job-2", Type: "youtube_clip.extract", RetryCount: 0, MaxRetries: 2}

	envelope := fmt.Errorf("extraction classified: %w — failure_details=%s",
		typedRetryableDispatchError{msg: "youtube extraction: retryable failure"}, `{"failure_class":"retryable"}`)

	w.finalizeJobDispatchError(context.Background(), j, w.id, "lease-2", 1, envelope)

	require.Equal(t, 1, rec.scheduled)
	require.Zero(t, rec.failed)
}

// TestFinalizeJobDispatchError_TerminalFailsAndDeadLetters pins the
// counterpart: a non-typed (terminal) error never schedules a retry.
func TestFinalizeJobDispatchError_TerminalFailsAndDeadLetters(t *testing.T) {
	rec := &recordingDispatchStore{}
	w := newDispatchGateWorker(rec)
	j := &job.Job{ID: "job-3", Type: "youtube_clip.extract", RetryCount: 0, MaxRetries: 2}

	w.finalizeJobDispatchError(context.Background(), j, w.id, "lease-3", 1,
		errors.New("youtube extraction: terminal failure"))

	require.Zero(t, rec.scheduled)
	require.Equal(t, 1, rec.failed)
	require.Equal(t, 1, rec.deadLetters)
}

// TestFinalizeJobDispatchError_RetryableButBudgetExhaustedDeadLetters pins
// that the retry budget still bounds a retryable verdict: retry_count ==
// max_retries is terminal (no infinite retry loop).
func TestFinalizeJobDispatchError_RetryableButBudgetExhaustedDeadLetters(t *testing.T) {
	rec := &recordingDispatchStore{}
	w := newDispatchGateWorker(rec)
	j := &job.Job{ID: "job-4", Type: "youtube_clip.extract", RetryCount: 2, MaxRetries: 2}

	w.finalizeJobDispatchError(context.Background(), j, w.id, "lease-4", 1,
		typedRetryableDispatchError{msg: "youtube extraction: retryable failure"})

	require.Zero(t, rec.scheduled, "the budget is exhausted")
	require.Equal(t, 1, rec.failed)
	require.Equal(t, 1, rec.deadLetters)
}
