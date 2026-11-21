package renderqueue

import (
	"context"
	"testing"
	"time"
)

// waiterClient is a queue client that exposes the event-driven completion
// capability. It records whether the wait used the waiter or fell back to Get.
type waiterClient struct {
	getCalls  int
	waitCalls int
	job       RenderQueueJob
}

func (c *waiterClient) Submit(context.Context, RenderQueueJob) error { return nil }

func (c *waiterClient) Get(context.Context, string) (RenderQueueJob, error) {
	c.getCalls++
	return c.job, nil
}

func (c *waiterClient) WaitTerminal(_ context.Context, id string) (RenderQueueJob, error) {
	c.waitCalls++
	job := c.job
	job.ID = id
	return job, nil
}

// pollClient is an older/degraded queue client: it has no waiter, so the only
// way to observe completion is the polling fallback.
type pollClient struct {
	getCalls int
	job      RenderQueueJob
}

func (c *pollClient) Submit(context.Context, RenderQueueJob) error { return nil }

func (c *pollClient) Get(context.Context, string) (RenderQueueJob, error) {
	c.getCalls++
	return c.job, nil
}

// TestWaitRenderQueueTerminalUsesWaiterAndDoesNotPoll pins the production
// completion path: when the client implements RenderQueueWaiter the wait parks
// on the state transition and never samples the job. This is the invariant that
// makes the poll interval fallback-only (perf clip.render PERF-04): lowering the
// interval cannot remove latency that does not exist on this path.
func TestWaitRenderQueueTerminalUsesWaiterAndDoesNotPoll(t *testing.T) {
	client := &waiterClient{job: RenderQueueJob{State: RenderQueueStateCompleted}}

	job, metrics, err := WaitRenderQueueTerminal(
		context.Background(), client, "job-waiter", 500*time.Millisecond)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if job.ID != "job-waiter" || job.State != RenderQueueStateCompleted {
		t.Fatalf("job = %+v, want completed job-waiter", job)
	}
	if client.waitCalls != 1 {
		t.Fatalf("WaitTerminal calls = %d, want 1", client.waitCalls)
	}
	if client.getCalls != 0 {
		t.Fatalf("Get calls = %d, want 0: the waiter path must not poll", client.getCalls)
	}
	if metrics.PollCount != 0 {
		t.Fatalf("PollCount = %d, want 0: the interval is fallback-only", metrics.PollCount)
	}
	if metrics.CompletionWait < 0 {
		t.Fatalf("CompletionWait = %v, want >= 0", metrics.CompletionWait)
	}
}

// TestWaitRenderQueueTerminalFallsBackToPolling keeps the additive contract
// honest: a client without the waiter still completes through the bounded poll
// loop, so the optimisation never becomes a requirement on the queue.
func TestWaitRenderQueueTerminalFallsBackToPolling(t *testing.T) {
	client := &pollClient{job: RenderQueueJob{State: RenderQueueStateCompleted}}

	job, metrics, err := WaitRenderQueueTerminal(
		context.Background(), client, "job-poll", time.Millisecond)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if job.State != RenderQueueStateCompleted {
		t.Fatalf("state = %q, want completed", job.State)
	}
	if client.getCalls != 1 {
		t.Fatalf("Get calls = %d, want 1", client.getCalls)
	}
	if metrics.PollCount != 1 {
		t.Fatalf("PollCount = %d, want 1", metrics.PollCount)
	}
}
