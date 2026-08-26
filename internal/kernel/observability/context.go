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

type stageContextKey struct{}

// WithStage binds an observability stage name to ctx so shared helpers that
// are reused across pipeline phases (e.g. the artifact-publish spine, which
// serves both the post_writer_finalize worker path and stock/vidrush
// publishers) can attribute their MeasureOperation records to the CALLER's
// stage instead of a hardcoded neutral value. Callers that do not set a
// stage keep the helper's default (typically StagePublish).
func WithStage(ctx context.Context, stage StageName) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, stageContextKey{}, stage)
}

// StageFrom returns the stage bound via WithStage, or "" when none is bound.
func StageFrom(ctx context.Context) StageName {
	if ctx == nil {
		return ""
	}
	stage, _ := ctx.Value(stageContextKey{}).(StageName)
	return stage
}

// StageOrDefault returns the stage bound via WithStage, or fallback when no
// stage is bound. Shared helpers (e.g. the artifact-publish spine) use it to
// attribute operations to the caller's stage while keeping a sensible default
// (StagePublish) for callers that do not tag their context.
func StageOrDefault(ctx context.Context, fallback StageName) StageName {
	if stage := StageFrom(ctx); stage != "" {
		return stage
	}
	return fallback
}
