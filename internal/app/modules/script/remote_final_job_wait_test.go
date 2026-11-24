package script

import (
	"context"
	"testing"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/stretchr/testify/require"
)

// The remote Master poll was wall time with no owner: the run appeared idle
// while waiting for a remote render. It is now a child-dependency wait on the
// render-queue component (the same component the artifact download uses).
func TestRecordRemoteFinalJobWait(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-remote-wait", AttemptID: "attempt-remote-wait"})
	ctx := kernobs.WithRun(context.Background(), run)

	started := time.Now()
	time.Sleep(2 * time.Millisecond)
	recordRemoteFinalJobWait(ctx, started)

	require.Len(t, run.Report().Waits, 1)
	wait := run.Report().Waits[0]
	require.Equal(t, kernobs.WaitChildDependency, wait.Kind)
	require.Equal(t, string(kernobs.ComponentRenderQueue), wait.Component)
	require.Greater(t, wait.DurationMs, int64(0))
}

// A non-blocking attach (waitToCompletion=false) returns immediately and must
// not create an interval.
func TestRecordRemoteFinalJobWaitSkipsNonBlockingAttach(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-remote-fast", AttemptID: "attempt-remote-fast"})
	ctx := kernobs.WithRun(context.Background(), run)

	recordRemoteFinalJobWait(ctx, time.Now())
	require.Empty(t, run.Report().Waits)
}
