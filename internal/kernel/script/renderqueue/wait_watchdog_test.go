package renderqueue

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// watchClient is a polling-only queue double (no WaitTerminal) that serves a
// scripted sequence of job snapshots and records cancellations.
type watchClient struct {
	jobs      []RenderQueueJob
	i         int
	cancelErr error
	cancelled []string
}

func (c *watchClient) Submit(context.Context, RenderQueueJob) error { return nil }

func (c *watchClient) Get(context.Context, string) (RenderQueueJob, error) {
	if len(c.jobs) == 0 {
		return RenderQueueJob{}, errors.New("no job scripted")
	}
	job := c.jobs[c.i]
	if c.i < len(c.jobs)-1 {
		c.i++
	}
	return job, nil
}

func (c *watchClient) Cancel(_ context.Context, id string) error {
	c.cancelled = append(c.cancelled, id)
	return c.cancelErr
}

func runningJob(startedAgo, lastFrameAgo time.Duration, frames, total int) RenderQueueJob {
	now := time.Now()
	return RenderQueueJob{
		ID: "job_child", State: "running",
		StartedAt: now.Add(-startedAgo),
		Progress: &RenderQueueProgress{
			FramesDone: frames, TotalFrames: total, LastFrameAt: now.Add(-lastFrameAgo),
		},
	}
}

// TestWatchedWaitAbortsAStalledRender is the measured case: a child that has
// not reported a frame in 4 minutes must be cancelled immediately, not waited
// out to 847 s.
func TestWatchedWaitAbortsAStalledRender(t *testing.T) {
	client := &watchClient{jobs: []RenderQueueJob{runningJob(5*time.Minute, 4*time.Minute, 12, 300)}}
	started := time.Now()
	job, _, err := WaitRenderQueueTerminalWatched(context.Background(), client, "job_child", 5*time.Millisecond, time.Second, time.Minute)
	if err == nil {
		t.Fatal("a render with no progress must be aborted")
	}
	if !errors.Is(err, ErrRenderQueueStalled) {
		t.Fatalf("error = %v, want ErrRenderQueueStalled", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("stall detection took %s, want it at the first observation", elapsed)
	}
	if len(client.cancelled) != 1 || client.cancelled[0] != "job_child" {
		t.Fatalf("cancel calls = %v, want exactly one on job_child", client.cancelled)
	}
	for _, want := range []string{"job_child", "frames 12/300", "4m0s"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("stall message %q must name %q", err.Error(), want)
		}
	}
	if job.ID != "job_child" {
		t.Fatalf("the stalled job handle must be returned, got %q", job.ID)
	}
}

// TestWatchedWaitNeverAbortsAProgressingRender pins the safety rule: slow is
// not stalled. The render keeps reporting new frames, so it is waited out.
func TestWatchedWaitNeverAbortsAProgressingRender(t *testing.T) {
	client := &watchClient{jobs: []RenderQueueJob{
		runningJob(10*time.Minute, 0, 10, 300),
		runningJob(10*time.Minute, 0, 11, 300),
		func() RenderQueueJob {
			job := runningJob(10*time.Minute, 0, 300, 300)
			job.State = "completed"
			return job
		}(),
	}}
	_, _, err := WaitRenderQueueTerminalWatched(context.Background(), client, "job_child", 5*time.Millisecond, time.Second, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("a progressing render must not be aborted, got %v", err)
	}
	if len(client.cancelled) != 0 {
		t.Fatalf("cancel calls = %v, want none", client.cancelled)
	}
}

// TestWatchedWaitLeavesAdmissionWaitToTheBudget: a job that has not started is
// queued, not stalled.
func TestWatchedWaitLeavesAdmissionWaitToTheBudget(t *testing.T) {
	client := &watchClient{jobs: []RenderQueueJob{{ID: "job_queued", State: RenderQueueStatePending}}}
	_, _, err := WaitRenderQueueTerminalWatched(context.Background(), client, "job_queued", 5*time.Millisecond, 40*time.Millisecond, time.Minute)
	if !errors.Is(err, ErrRenderQueuePending) {
		t.Fatalf("error = %v, want ErrRenderQueuePending (the budget, not a stall)", err)
	}
	if len(client.cancelled) != 0 {
		t.Fatalf("a queued job must never be cancelled, got %v", client.cancelled)
	}
}

// TestWatchedWaitQueuedItemIsNotAStall is the OVL-01 case: an item waiting for
// a free GPU lane reports frames=0 and an old timestamp, which is NOT a stall.
// Its age carries no progress information, so the watchdog must leave it to the
// budget and never cancel it.
func TestWatchedWaitQueuedItemIsNotAStall(t *testing.T) {
	old := time.Now().Add(-time.Hour)
	client := &watchClient{jobs: []RenderQueueJob{{
		ID:       "job_queued",
		State:    RenderQueueStatePending,
		QueuedAt: old,
		Progress: &RenderQueueProgress{FramesDone: 0, TotalFrames: 120, LastFrameAt: old},
	}}}
	_, _, err := WaitRenderQueueTerminalWatched(context.Background(), client, "job_queued", 5*time.Millisecond, 40*time.Millisecond, time.Millisecond)
	if !errors.Is(err, ErrRenderQueuePending) {
		t.Fatalf("error = %v, want ErrRenderQueuePending (the budget, not a stall)", err)
	}
	if len(client.cancelled) != 0 {
		t.Fatalf("a queued item must never be cancelled, got %v", client.cancelled)
	}
}

// TestWatchedWaitRunningItemStallsPastTheThreshold keeps the safety half: once
// the job is RUNNING and its last frame is older than the window, it is still
// cancelled so a render that holds the lane without advancing frees it.
func TestWatchedWaitRunningItemStallsPastTheThreshold(t *testing.T) {
	client := &watchClient{jobs: []RenderQueueJob{runningJob(10*time.Minute, 10*time.Minute, 0, 120)}}
	_, _, err := WaitRenderQueueTerminalWatched(context.Background(), client, "job_child", 5*time.Millisecond, time.Minute, 20*time.Millisecond)
	if !errors.Is(err, ErrRenderQueueStalled) {
		t.Fatalf("error = %v, want ErrRenderQueueStalled", err)
	}
	if len(client.cancelled) != 1 || client.cancelled[0] != "job_child" {
		t.Fatalf("cancel calls = %v, want exactly one on job_child", client.cancelled)
	}
}

// TestWatchedWaitStallClockStartsAtRunningAdmission pins "the threshold counts
// from RUNNING, not from submit": the job is queued well past the window (no
// StartedAt reported) and is then admitted; the window restarts at admission,
// so the first RUNNING observation cannot be mistaken for a stall.
func TestWatchedWaitStallClockStartsAtRunningAdmission(t *testing.T) {
	queued := RenderQueueJob{ID: "job_admitted", State: RenderQueueStatePending}
	running := RenderQueueJob{ID: "job_admitted", State: RenderQueueStateRunning}
	done := RenderQueueJob{ID: "job_admitted", State: RenderQueueStateCompleted}
	client := &watchClient{jobs: []RenderQueueJob{queued, queued, queued, queued, running, done}}
	job, _, err := WaitRenderQueueTerminalWatched(context.Background(), client, "job_admitted", 5*time.Millisecond, time.Second, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("an admitted render must not be judged on its queued wait, got %v", err)
	}
	if job.State != RenderQueueStateCompleted {
		t.Fatalf("state = %q, want completed", job.State)
	}
	if len(client.cancelled) != 0 {
		t.Fatalf("cancel calls = %v, want none", client.cancelled)
	}
}

// TestWatchedWaitReportsCancelFailure keeps the lane state visible: a stall the
// watchdog could not cancel must say so.
func TestWatchedWaitReportsCancelFailure(t *testing.T) {
	client := &watchClient{
		jobs:      []RenderQueueJob{runningJob(5*time.Minute, 4*time.Minute, 1, 10)},
		cancelErr: errors.New("queue refused"),
	}
	_, _, err := WaitRenderQueueTerminalWatched(context.Background(), client, "job_child", 5*time.Millisecond, time.Second, time.Minute)
	if !errors.Is(err, ErrRenderQueueStalled) {
		t.Fatalf("error = %v, want ErrRenderQueueStalled", err)
	}
	if !strings.Contains(err.Error(), "cancel failed: queue refused") {
		t.Fatalf("error %q must report the failed cancel", err.Error())
	}
}
