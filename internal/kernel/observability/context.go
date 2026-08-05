package observability

import "context"

type runContextKey struct{}

// WithRun binds run to ctx so any code downstream (adapters, processors,
// child jobs) can measure stages and operations through
// MeasureStage/MeasureOperation without holding the run directly:
//
//	run := observer.StartRun(ctx, observability.RunInfo{...})
//	ctx = observability.WithRun(ctx, run)
//	defer run.Finish()
func WithRun(ctx context.Context, run *Run) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, runContextKey{}, run)
}

// FromContext returns the run bound to ctx, or nil when none is bound.
// Nil-tolerant on both ctx and the bound value.
func FromContext(ctx context.Context) *Run {
	if ctx == nil {
		return nil
	}
	run, _ := ctx.Value(runContextKey{}).(*Run)
	return run
}

// RunFromContext is the strict variant: it reports whether a run is bound.
func RunFromContext(ctx context.Context) (*Run, bool) {
	run := FromContext(ctx)
	return run, run != nil
}
