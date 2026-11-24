package scriptgeneration

import (
	"context"
	"testing"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/stretchr/testify/require"
)

// The publication drain used to be invisible: complete() joined the async
// upload pool with no operation and no wait, so the interval fell into the
// run's unattributed bucket. It is now a typed outbox-delivery wait on the
// Drive component.
func TestRecordPublicationDrainWait(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-drain", AttemptID: "attempt-drain"})
	ctx := kernobs.WithRun(context.Background(), run)

	started := time.Now()
	time.Sleep(2 * time.Millisecond)
	recordPublicationDrainWait(ctx, started)

	wait := findRunWait(t, run, kernobs.WaitOutboxDelivery, kernobs.ComponentDrive)
	require.NotNil(t, wait, "the publication drain must be recorded, got %+v", run.Report().Waits)
	require.Greater(t, wait.DurationMs, int64(0))
}

// A drain with nothing in flight returns immediately; recording it would flood
// the report with zero-length intervals and distort the wait counts.
func TestRecordPublicationDrainWaitSkipsNonBlockingDrain(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-drain-fast", AttemptID: "attempt-drain-fast"})
	ctx := kernobs.WithRun(context.Background(), run)

	recordPublicationDrainWait(ctx, time.Now())
	require.Empty(t, run.Report().Waits)
}

// Instrumentation must stay a no-op without a bound run (it must never panic
// or block a publication).
func TestRecordPublicationDrainWaitWithoutRun(t *testing.T) {
	recordPublicationDrainWait(context.Background(), time.Now().Add(-time.Millisecond))
}
