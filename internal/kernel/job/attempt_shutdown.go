// Package job — attempt_shutdown.go: the canonical "the WORKER stopped this
// attempt, the job did not fail" seam.
//
// A graceful restart cancels the worker lifecycle context, which propagates
// into every in-flight jobCtx. Handlers then return a cancellation error that
// describes the WORKER'S exit, not a verdict on the job: the same immutable
// request resumes from the run's checkpoint on the next attempt. Treating it
// as a failure is what dead-lettered healthy runs during a restart (measured
// 2026-11-22: the r26b durable run was left RUNNING/UNKNOWN_STAGE while its
// job went FAILED/NO_ERROR_CODE straight to the dead-letter queue, and every
// further restart spent another attempt).
//
// A USER cancellation must keep failing the job, and it also arrives as
// context.Canceled, so the error value alone cannot tell the two apart. The
// distinguishing fact lives where cancellation originates: the worker's parent
// context is done only when the process is stopping. The probe is a closure
// rather than a boolean because shutdown can begin at any point during
// Dispatch.
package job

import "context"

type workerStopProbeKey struct{}

// WithWorkerStopProbe binds a live "the worker lifecycle context is done"
// probe to ctx. Worker.runJob installs it once per attempt; a ctx without a
// probe reports false, so direct callers (tests, tooling) keep the historical
// behaviour.
func WithWorkerStopProbe(ctx context.Context, probe func() bool) context.Context {
	if ctx == nil || probe == nil {
		return ctx
	}
	return context.WithValue(ctx, workerStopProbeKey{}, probe)
}

// WorkerStopped reports whether the attempt was interrupted by the worker
// lifecycle (shutdown/restart) rather than by the job's own cancellation,
// deadline or handler verdict.
func WorkerStopped(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	probe, ok := ctx.Value(workerStopProbeKey{}).(func() bool)
	return ok && probe != nil && probe()
}
