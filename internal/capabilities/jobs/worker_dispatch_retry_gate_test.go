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

	jobscheduling "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/scheduling"
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
	// finalized records every canonical FinalizeAttempt command the worker
	// issued — the deferral path is the only one that uses it today, so the
	// recorded outcome IS the assertion.
	finalized []job.FinalizeAttemptCommand
}

func (r *recordingDispatchStore) FinalizeAttempt(_ context.Context, cmd job.FinalizeAttemptCommand) (job.FinalizeAttemptResult, error) {
	r.finalized = append(r.finalized, cmd)
	return job.FinalizeAttemptResult{JobID: cmd.JobID, FinalStatus: job.StatusRetryWait, NewRevision: cmd.ExpectedRevision + 1}, nil
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

// ── Deferral gate: a WAIT is neither a retry nor a failure ────────────────
//
// The deferral gate is checked BEFORE retry.IsTransient / DecideRetry, because
// a handler that is waiting on work outside the jobs plane (a remote render, a
// provider window) has not failed: charging that wait to the retry budget is how
// a long external wait becomes a terminal FAILED job.

func TestFinalizeJobDispatchError_DeferralDefersWithoutTouchingTheRetryBudget(t *testing.T) {
	rec := &recordingDispatchStore{}
	w := newDispatchGateWorker(rec)
	j := &job.Job{ID: "job-defer-1", Type: "clip.render", RetryCount: 0, MaxRetries: 2}

	w.finalizeJobDispatchError(context.Background(), j, w.id, "lease-d1", 7,
		job.DeferredAfter(45*time.Second, "remote render still running"))

	require.Len(t, rec.finalized, 1, "a deferral is persisted through the canonical FinalizeAttempt")
	cmd := rec.finalized[0]
	require.Equal(t, job.OutcomeDeferred, cmd.Outcome)
	require.Equal(t, 45*time.Second, cmd.Backoff, "the handler's delay reaches the store verbatim")
	require.Equal(t, "remote render still running", cmd.ErrorMessage)
	require.Equal(t, 7, cmd.ExpectedRevision, "the CAS fence uses the revision the worker holds")
	require.Equal(t, w.id, cmd.WorkerID)
	require.Equal(t, "lease-d1", cmd.LeaseID)
	require.Equal(t, "job_deferred", cmd.EventType)
	require.Zero(t, rec.scheduled, "a wait must not spend a retry")
	require.Zero(t, rec.failed, "a wait must not fail the job")
	require.Zero(t, rec.deadLetters, "a wait must never be dead-lettered")
}

// TestFinalizeJobDispatchError_DeferralWithoutADelayUsesTheDeploymentDefault:
// the handler states the fact (I am waiting), never the cadence. The row MUST
// still carry an instant — the store rejects a deferral without one — so the
// worker fills the scheduling default.
func TestFinalizeJobDispatchError_DeferralWithoutADelayUsesTheDeploymentDefault(t *testing.T) {
	rec := &recordingDispatchStore{}
	w := newDispatchGateWorker(rec)
	j := &job.Job{ID: "job-defer-2", Type: "clip.render", RetryCount: 0, MaxRetries: 2}

	w.finalizeJobDispatchError(context.Background(), j, w.id, "lease-d2", 1,
		job.Deferred("GPU lane busy"))

	require.Len(t, rec.finalized, 1)
	require.Equal(t, jobscheduling.DefaultDeferralDelay, rec.finalized[0].Backoff)
	require.Zero(t, rec.scheduled)
	require.Zero(t, rec.failed)
}

// TestFinalizeJobDispatchError_DeferralAtAnExhaustedBudgetStillWaits pins that
// the deferral gate runs BEFORE the retry-budget gate: the whole point is that a
// wait spends no budget, so "retries left" cannot gate it. Without this order a
// render still in flight at retry_count == max_retries would be failed while its
// remote work is alive.
func TestFinalizeJobDispatchError_DeferralAtAnExhaustedBudgetStillWaits(t *testing.T) {
	rec := &recordingDispatchStore{}
	w := newDispatchGateWorker(rec)
	j := &job.Job{ID: "job-defer-4", Type: "clip.render", RetryCount: 2, MaxRetries: 2}

	w.finalizeJobDispatchError(context.Background(), j, w.id, "lease-d4", 1,
		job.DeferredAfter(time.Minute, "still rendering"))

	require.Len(t, rec.finalized, 1, "an exhausted retry budget must not block a wait")
	require.Equal(t, job.OutcomeDeferred, rec.finalized[0].Outcome)
	require.Zero(t, rec.scheduled)
	require.Zero(t, rec.failed)
	require.Zero(t, rec.deadLetters)
}

// TestFinalizeJobDispatchError_DeferralBeatsTheTransientProbe pins the ORDER of
// the gate: an error that is BOTH typed-retryable and a deferral must defer. A
// retry would spend the budget on work that has not failed, and after two of
// those the next attempt would be terminal while the render is still running.
func TestFinalizeJobDispatchError_DeferralBeatsTheTransientProbe(t *testing.T) {
	rec := &recordingDispatchStore{}
	w := newDispatchGateWorker(rec)
	j := &job.Job{ID: "job-defer-3", Type: "clip.render", RetryCount: 1, MaxRetries: 2}

	both := fmt.Errorf("clip.render: settle: %w: %w",
		typedRetryableDispatchError{msg: "remote poll: transient"}, job.DeferredAfter(time.Minute, "render still running"))

	w.finalizeJobDispatchError(context.Background(), j, w.id, "lease-d3", 2, both)

	require.Len(t, rec.finalized, 1, "the deferral must win over the retry classification")
	require.Equal(t, job.OutcomeDeferred, rec.finalized[0].Outcome)
	require.Zero(t, rec.scheduled)
	require.Zero(t, rec.failed)
}

// TestFinalizeJobDispatchError_DeferralFailureIsLoudAndNotACompletion: if the
// deferral cannot be persisted (CAS loss / store error) the worker must NOT
// report the job as completed or failed — it logs and leaves the row leased, so
// the reaper requeues it and the handler runs again.
func TestFinalizeJobDispatchError_DeferralFailureIsLoudAndNotACompletion(t *testing.T) {
	rec := &recordingDispatchStore{}
	fail := &failingFinalizeStore{recordingDispatchStore: rec}
	w := &Worker{id: "retry-gate-worker", repo: fail, log: zap.NewNop()}
	j := &job.Job{ID: "job-defer-4", Type: "clip.render", RetryCount: 0, MaxRetries: 2}

	w.finalizeJobDispatchError(context.Background(), j, w.id, "lease-d4", 1,
		job.DeferredAfter(time.Minute, "render still running"))

	require.Equal(t, 1, fail.calls, "the worker must attempt the canonical deferral write")
	require.Empty(t, rec.finalized, "a rejected deferral is not a persisted one")
	require.Zero(t, rec.scheduled, "a failed deferral must not be quietly turned into a retry")
	require.Zero(t, rec.failed)
	require.Zero(t, rec.deadLetters)
}

// failingFinalizeStore delegates every other verb to the recording store and
// makes the canonical deferral write fail with the lease sentinel every worker
// hits when another owner reclaimed the row.
type failingFinalizeStore struct {
	*recordingDispatchStore
	calls int
}

func (f *failingFinalizeStore) FinalizeAttempt(_ context.Context, _ job.FinalizeAttemptCommand) (job.FinalizeAttemptResult, error) {
	f.calls++
	return job.FinalizeAttemptResult{}, job.ErrLeaseLost
}
