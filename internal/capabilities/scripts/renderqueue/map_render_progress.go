package renderqueue

import (
	"context"
	"time"
)

// MapRenderProgress is one observable transition of the native dynamic-map
// subprocess. The renderer reports start, heartbeat and exit; the runner
// projects these events onto jobs.progress/current_detail so /full is not
// silent while a map is still rendering.
type MapRenderProgress struct {
	Phase   string
	PID     int
	Elapsed time.Duration
	Result  string
	// ExitCode is -1 until the child has a terminal ProcessState.
	ExitCode int
	Err      error
}

type mapRenderProgressKey struct{}

// WithMapRenderProgress attaches the observer to ctx. A nil observer is a
// no-op, so callers can wire it unconditionally.
func WithMapRenderProgress(ctx context.Context, observer func(MapRenderProgress)) context.Context {
	if ctx == nil || observer == nil {
		return ctx
	}
	return context.WithValue(ctx, mapRenderProgressKey{}, observer)
}

func reportMapRenderProgress(ctx context.Context, event MapRenderProgress) {
	if ctx == nil {
		return
	}
	observer, _ := ctx.Value(mapRenderProgressKey{}).(func(MapRenderProgress))
	if observer != nil {
		observer(event)
	}
}

// ReportMapRenderProgress publishes an event from an owner of the native map
// subprocess. Exported within the internal scripts module so orchestration
// adapters and contract tests can exercise the same context observer.
func ReportMapRenderProgress(ctx context.Context, event MapRenderProgress) {
	reportMapRenderProgress(ctx, event)
}
