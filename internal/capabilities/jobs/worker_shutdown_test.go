package jobs

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// canceledJobCtx returns a job context that has already been cancelled, with
// and without the live lifecycle probe that marks a worker shutdown.
func canceledJobCtx(withShutdownProbe bool) context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if withShutdownProbe {
		return job.WithWorkerStopProbe(ctx, func() bool { return true })
	}
	return ctx
}

// TestWorkerShutdownInterruption pins the classification rule: only an attempt
// whose context died WITH the worker lifecycle becomes a deferral. A user
// cancellation, a job timeout and a healthy attempt keep their own semantics.
func TestWorkerShutdownInterruption(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		ctx     context.Context
		err     error
		wantDef bool
	}{
		{name: "no error", ctx: context.Background(), err: nil},
		{name: "live worker", ctx: context.Background(), err: fmt.Errorf("boom")},
		{name: "user cancel", ctx: canceledJobCtx(false), err: fmt.Errorf("cancelled: %w", context.Canceled)},
		{name: "shutdown cancel", ctx: canceledJobCtx(true), err: fmt.Errorf("cancelled: %w", context.Canceled), wantDef: true},
		{
			// The r26b shape: the enqueue failure's text does not wrap
			// context.Canceled at all, but the job context died with the worker.
			name: "shutdown with opaque handler error", ctx: canceledJobCtx(true),
			err: fmt.Errorf("fixed-media localized render enqueue failed: pipe closed"), wantDef: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := workerShutdownInterruption(tc.ctx, tc.err)
			if !tc.wantDef {
				require.NoError(t, got)
				return
			}
			require.Error(t, got)
			deferral, ok := job.AsDeferral(got)
			require.True(t, ok, "a shutdown interruption must be a deferral, got %v", got)
			require.NotEmpty(t, deferral.Reason)
		})
	}
}

// TestFinalizeJobDispatchError_ShutdownDefersInsteadOfDeadLettering is the
// regression pin for the r26b restart: a run interrupted by pipelinegen
// shutting down must hand the attempt back (OutcomeDeferred, retry_count
// untouched, `deferred_until` set) instead of failing the job and dead-lettering
// it with UNKNOWN_STAGE/NO_ERROR_CODE.
func TestFinalizeJobDispatchError_ShutdownDefersInsteadOfDeadLettering(t *testing.T) {
	rec := &recordingDispatchStore{}
	w := newDispatchGateWorker(rec)
	j := &job.Job{ID: "job-shutdown", Type: "script.generate", RetryCount: 0, MaxRetries: 3}

	jobCtx := canceledJobCtx(true)
	dispatchErr := fmt.Errorf("script.generate cancelled at handler-entry: %w", context.Canceled)
	interruption := workerShutdownInterruption(jobCtx, dispatchErr)
	require.Error(t, interruption)

	w.finalizeJobDispatchError(context.Background(), j, w.id, "lease-shutdown", 1, interruption)

	require.Len(t, rec.finalized, 1, "the attempt must be finalized through the canonical deferral path")
	require.Equal(t, job.OutcomeDeferred, rec.finalized[0].Outcome)
	require.Zero(t, rec.failed, "a shutdown must not mark the job FAILED")
	require.Zero(t, rec.deadLetters, "a shutdown must not dead-letter the job")
	require.Zero(t, rec.scheduled, "a deferral spends no retry budget")
}

// TestFinalizeJobDispatchError_UserCancelStillFails pins the boundary: a
// caller-initiated cancellation keeps failing (and dead-lettering) the job, so
// the shutdown allowance cannot be used to resurrect work nobody wants.
func TestFinalizeJobDispatchError_UserCancelStillFails(t *testing.T) {
	rec := &recordingDispatchStore{}
	w := newDispatchGateWorker(rec)
	j := &job.Job{ID: "job-user-cancel", Type: "script.generate", RetryCount: 0, MaxRetries: 3}

	// No lifecycle probe: this is the job's own cancel func firing.
	w.finalizeJobDispatchError(context.Background(), j, w.id, "lease-cancel", 1,
		fmt.Errorf("script.generate cancelled at handler-entry: %w", context.Canceled))

	require.Empty(t, rec.finalized)
	require.Equal(t, 1, rec.failed)
	require.Equal(t, 1, rec.deadLetters)
}
