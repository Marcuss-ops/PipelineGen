package renderinggen

import (
	"context"
	"errors"
	"testing"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	renderqueue "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/renderqueue"
)

type stalledSettleQueue struct {
	cancelled int
}

func (q *stalledSettleQueue) Submit(context.Context, renderqueue.RenderQueueJob) error {
	return nil
}

func (q *stalledSettleQueue) Get(context.Context, string) (renderqueue.RenderQueueJob, error) {
	now := time.Now()
	return renderqueue.RenderQueueJob{
		ID:        "job-stalled",
		State:     "running",
		StartedAt: now.Add(-10 * time.Minute),
		Progress: &renderqueue.RenderQueueProgress{
			FramesDone:  1,
			TotalFrames: 100,
			LastFrameAt: now.Add(-10 * time.Minute),
		},
	}, nil
}

func (q *stalledSettleQueue) Cancel(context.Context, string) error {
	q.cancelled++
	return nil
}

// TestSettleWithoutBudgetStillUsesWatchdog pins CR-03: after the settle window
// the worker passes budget 0, and that path must still cancel a render whose
// progress stopped, instead of waiting for the 30-minute job timeout.
func TestSettleWithoutBudgetStillUsesWatchdog(t *testing.T) {
	queue := &stalledSettleQueue{}
	executor, err := NewClipRenderExecutor(queue)
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	_, err = executor.SettleWithin(context.Background(), validClipPlan(t), "job-stalled", 0)
	if !errors.Is(err, cliprender.ErrRenderStalled) {
		t.Fatalf("error = %v, want ErrRenderStalled", err)
	}
	if queue.cancelled != 1 {
		t.Fatalf("cancel calls = %d, want 1", queue.cancelled)
	}
}
