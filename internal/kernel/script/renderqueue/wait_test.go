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

// slicingWaiter simulates the production long poll: it parks until its slice
// expires, so the watchdog must sample progress with a Get at the boundary.
type slicingWaiter struct {
	getCalls  int
	waitCalls int
	job       RenderQueueJob
}

func (c *slicingWaiter) Submit(context.Context, RenderQueueJob) error { return nil }

func (c *slicingWaiter) Get(_ context.Context, id string) (RenderQueueJob, error) {
	c.getCalls++
	job := c.job
	job.ID = id
	return job, nil
}

func (c *slicingWaiter) WaitTerminal(ctx context.Context, _ string) (RenderQueueJob, error) {
	c.waitCalls++
	<-ctx.Done()
	return RenderQueueJob{}, ctx.Err()
}

// progressWaiter simulates the production progress-carrying long poll: each
// call returns the current running document (with Progress) after a short
// slice, and the terminal document once the simulated render completes. It does
// not implement Get through the wait, so any sampling read would show up in
// getCalls.
type progressWaiter struct {
	waitCalls int
	getCalls  int
	start     time.Time
	duration  time.Duration
	step      time.Duration
}

func (c *progressWaiter) Submit(context.Context, RenderQueueJob) error { return nil }

func (c *progressWaiter) Get(context.Context, string) (RenderQueueJob, error) {
	c.getCalls++
	return RenderQueueJob{}, nil
}

func (c *progressWaiter) WaitTerminal(ctx context.Context, _ string) (RenderQueueJob, error) {
	<-ctx.Done()
	return RenderQueueJob{}, ctx.Err()
}

func (c *progressWaiter) WaitProgress(ctx context.Context, id string) (RenderQueueJob, error) {
	c.waitCalls++
	if c.start.IsZero() {
		c.start = time.Now()
	}
	if elapsed := time.Since(c.start); elapsed < c.duration {
		timer := time.NewTimer(c.step)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		}
	}
	if time.Since(c.start) >= c.duration {
		return RenderQueueJob{ID: id, State: RenderQueueStateCompleted}, nil
	}
	return RenderQueueJob{
		ID: id, State: "running",
		StartedAt: time.Now().Add(-time.Second),
		Progress: &RenderQueueProgress{
			FramesDone: c.waitCalls * 10, TotalFrames: 100, LastFrameAt: time.Now(),
		},
	}, nil
}

// TestWaitRenderQueueTerminalWatchedUsesProgressFromLongPoll is the CR-03
// request-count proof for the progress-carrying wait: a 90 ms render (the
// production 90 s case scaled down) completes with one request per 30 ms slice
// and ZERO sampling GETs. The previous path paid a park plus a GET per slice.
func TestWaitRenderQueueTerminalWatchedUsesProgressFromLongPoll(t *testing.T) {
	previous := watchedProgressSlice
	watchedProgressSlice = 30 * time.Millisecond
	defer func() { watchedProgressSlice = previous }()

	client := &progressWaiter{duration: 90 * time.Millisecond, step: 10 * time.Millisecond}
	job, metrics, err := WaitRenderQueueTerminalWatched(
		context.Background(), client, "job-progress", 500*time.Millisecond, 0, time.Minute)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if job.State != RenderQueueStateCompleted {
		t.Fatalf("state = %q, want completed", job.State)
	}
	if client.getCalls != 0 {
		t.Fatalf("Get calls = %d: a progress-carrying long poll must not add sampling GETs", client.getCalls)
	}
	if client.waitCalls > 10 {
		t.Fatalf("wait calls = %d, want <= 10 for a 90 ms render (production: 90 s at 30 s slices)", client.waitCalls)
	}
	if metrics.PollCount != client.waitCalls {
		t.Fatalf("PollCount = %d, wait calls = %d", metrics.PollCount, client.waitCalls)
	}
}

// TestWaitRenderQueueTerminalWatchedParksInSlices is the fallback request-count
// proof: a client without the progress-carrying wait parks on the long poll and
// samples progress once per slice. Two calls total (one park + one Get) replace
// the previous 500 ms GET storm.
func TestWaitRenderQueueTerminalWatchedParksInSlices(t *testing.T) {
	previous := watchedProgressSlice
	watchedProgressSlice = 5 * time.Millisecond
	defer func() { watchedProgressSlice = previous }()

	client := &slicingWaiter{job: RenderQueueJob{State: RenderQueueStateCompleted}}
	job, metrics, err := WaitRenderQueueTerminalWatched(
		context.Background(), client, "job-slice", 500*time.Millisecond, 0, time.Hour)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if job.ID != "job-slice" || job.State != RenderQueueStateCompleted {
		t.Fatalf("job = %+v, want completed job-slice", job)
	}
	if client.waitCalls != 1 || client.getCalls != 1 {
		t.Fatalf("wait calls = %d, get calls = %d; want 1 park + 1 progress sample", client.waitCalls, client.getCalls)
	}
	if metrics.PollCount != 2 {
		t.Fatalf("PollCount = %d, want 2 (one park + one sample)", metrics.PollCount)
	}
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
