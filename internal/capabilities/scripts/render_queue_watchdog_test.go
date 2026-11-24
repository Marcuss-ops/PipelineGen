package scriptgeneration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script/renderqueue"
	"github.com/stretchr/testify/require"
)

// stalledQueueClient reports a render that has STARTED but never advances: the
// shape the watchdog exists for. It exposes no waiter capability, so the wait
// samples it through Get exactly like an older deployment.
type stalledQueueClient struct {
	startedAt time.Time
}

func (c *stalledQueueClient) Submit(context.Context, RenderQueueJob) error { return nil }

func (c *stalledQueueClient) Get(context.Context, string) (RenderQueueJob, error) {
	return RenderQueueJob{
		ID:        "overlay-job",
		State:     "running",
		StartedAt: c.startedAt,
		Progress:  nil,
	}, nil
}

// TestOverlayCompletionWaitAbortsAStalledRender certifies WST-05: the overlay
// completion wait carries the 180 s progress watchdog. Before this, the path
// used the unwatched long poll, which is how one item parked a worker lane for
// 1.264 s with no stall deadline.
func TestOverlayCompletionWaitAbortsAStalledRender(t *testing.T) {
	enqueuer := &QueueRenderEnqueuer{
		client:       &stalledQueueClient{startedAt: time.Now().Add(-time.Hour)},
		pollInterval: 5 * time.Millisecond,
		stallAfter:   40 * time.Millisecond,
	}
	started := time.Now()
	_, _, err := enqueuer.waitForCompletion(context.Background(), "overlay-job")
	require.Error(t, err)
	require.True(t, errors.Is(err, renderqueue.ErrRenderQueueStalled), "the wait must report the typed stall, got: %v", err)
	require.Less(t, time.Since(started), 2*time.Second, "a stalled render must not park the lane")
}

// TestOverlayCompletionWaitDefaultWatchdog pins the production default: without
// an explicit override the wait resolves to the certified 180 s window, so a
// slow-but-advancing render is never cancelled early.
func TestOverlayCompletionWaitDefaultWatchdog(t *testing.T) {
	enqueuer := &QueueRenderEnqueuer{}
	require.Equal(t, renderqueue.DefaultRenderStallAfter, enqueuer.renderStallAfter())
	require.Equal(t, renderqueue.DefaultRenderStallAfter, (&QueueRenderEnqueuer{stallAfter: -time.Second}).renderStallAfter())
}
