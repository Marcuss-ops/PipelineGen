package renderqueue

import (
	"context"
	"errors"
	"testing"
	"time"
)

// admissionClient models the worker's GPU-lane admission wait: the job is
// already RUNNING with an old StartedAt, but the worker publishes frames=0
// liveness heartbeats until a lane is free. The final Get completes the job.
type admissionClient struct {
	calls  int
	recent bool
}

func (c *admissionClient) Submit(context.Context, RenderQueueJob) error { return nil }

func (c *admissionClient) Get(_ context.Context, id string) (RenderQueueJob, error) {
	c.calls++
	if c.calls > 1 {
		return RenderQueueJob{ID: id, State: RenderQueueStateCompleted}, nil
	}
	lastFrameAt := time.Now().Add(-time.Hour)
	if c.recent {
		lastFrameAt = time.Now()
	}
	return RenderQueueJob{
		ID:        id,
		State:     "running",
		StartedAt: time.Now().Add(-time.Hour),
		Progress: &RenderQueueProgress{
			FramesDone: 0, TotalFrames: 120, LastFrameAt: lastFrameAt,
		},
	}, nil
}

// queuedClient models a job that has not been claimed yet: no StartedAt and no
// progress at all. Absolute age is never a stall signal on its own.
type queuedClient struct {
	calls int
}

func (c *queuedClient) Submit(context.Context, RenderQueueJob) error { return nil }

func (c *queuedClient) Get(_ context.Context, id string) (RenderQueueJob, error) {
	c.calls++
	if c.calls > 2 {
		return RenderQueueJob{ID: id, State: RenderQueueStateCompleted}, nil
	}
	return RenderQueueJob{ID: id, State: "pending"}, nil
}

// TestWaitRenderQueueTerminalWatchedDoesNotStallOnAdmissionHeartbeat pins the
// OVL-01 rule: while the worker is waiting for a free GPU lane it reports
// frames=0 with a fresh last-frame timestamp, and the watchdog must treat that
// as an admission wait, not a stall, even though StartedAt is an hour old.
func TestWaitRenderQueueTerminalWatchedDoesNotStallOnAdmissionHeartbeat(t *testing.T) {
	client := &admissionClient{recent: true}
	job, _, err := WaitRenderQueueTerminalWatched(
		context.Background(), client, "job-admission", time.Millisecond, 0, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if job.State != RenderQueueStateCompleted {
		t.Fatalf("state = %q, want completed", job.State)
	}
	if client.calls != 2 {
		t.Fatalf("Get calls = %d, want 2", client.calls)
	}
}

// TestWaitRenderQueueTerminalWatchedStallsRunningRenderWithoutProgress keeps
// the safety half of the same rule: once the lane is held and the heartbeat
// stops, a render whose last frame is older than the window IS cancelled.
func TestWaitRenderQueueTerminalWatchedStallsRunningRenderWithoutProgress(t *testing.T) {
	client := &admissionClient{recent: false}
	_, _, err := WaitRenderQueueTerminalWatched(
		context.Background(), client, "job-stuck", time.Millisecond, 0, 20*time.Millisecond)
	if !errors.Is(err, ErrRenderQueueStalled) {
		t.Fatalf("err = %v, want ErrRenderQueueStalled", err)
	}
	var stalled *RenderStalledError
	if !errors.As(err, &stalled) {
		t.Fatalf("err = %v, want *RenderStalledError", err)
	}
	if stalled.JobID != "job-stuck" || stalled.FramesDone != 0 {
		t.Fatalf("stalled = %+v, want job-stuck at frames 0", stalled)
	}
}

// TestWaitRenderQueueTerminalWatchedDoesNotStallQueuedJob proves the other half
// of the OVL-01 acceptance: a job that was never claimed (no StartedAt, no
// progress) may wait far longer than the stall window without being aborted.
func TestWaitRenderQueueTerminalWatchedDoesNotStallQueuedJob(t *testing.T) {
	client := &queuedClient{}
	job, _, err := WaitRenderQueueTerminalWatched(
		context.Background(), client, "job-queued", time.Millisecond, 0, time.Nanosecond)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if job.State != RenderQueueStateCompleted {
		t.Fatalf("state = %q, want completed", job.State)
	}
}
