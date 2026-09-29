// Package scriptgeneration — render_queue_bounded_wait_test.go pins the
// deferrable wait that makes a settle re-dispatchable.
//
// The contract under test is the ONE that decides whether a clip render pins a
// worker lane for its whole duration:
//
//   - inside the budget, a terminal render is returned exactly like the
//     blocking wait (no behaviour change for the common case);
//   - past the budget, the caller gets ErrRenderQueuePending WITH the job so it
//     can hand its attempt back as a deferral (a wait that spends no retry);
//   - budget 0 keeps the historical blocking contract byte for byte, because
//     every pre-existing caller passes no budget;
//   - a caller-side cancellation is NEVER reported as a wait — an aborted run
//     must not be re-dispatched as if the render were still coming.
package scriptgeneration

import (
	"context"
	"errors"
	"testing"
	"time"
)

// renderQueueStub answers Get with QUEUED/RUNNING until terminalAfter has
// elapsed, then COMPLETED. withWaiter additionally implements the event-driven
// port, whose WaitTerminal parks until the context ends (exactly what a
// long-poll does when the render has not finished).
type renderQueueStub struct {
	started       time.Time
	terminalAfter time.Duration
	withWaiter    bool
	gets          int
}

func newRenderQueueStub(terminalAfter time.Duration) *renderQueueStub {
	return &renderQueueStub{started: time.Now(), terminalAfter: terminalAfter}
}

func (s *renderQueueStub) Submit(context.Context, RenderQueueJob) error { return nil }

func (s *renderQueueStub) Get(_ context.Context, id string) (RenderQueueJob, error) {
	s.gets++
	// "running" is spelled as the raw wire state: the capability declares only
	// the TERMINAL states as constants, and that is deliberate (a non-terminal
	// state is exactly what this wait must not interpret).
	state := "running"
	if time.Since(s.started) >= s.terminalAfter {
		state = RenderQueueStateCompleted
	}
	return RenderQueueJob{ID: id, State: state}, nil
}

// waitTerminal is deliberately LOWER-CASE: the type must not accidentally
// satisfy the optional event-driven port (Go interfaces are structural, so a
// stray exported method would silently switch the code path under test).
func (s *renderQueueStub) waitTerminal(ctx context.Context, id string) (RenderQueueJob, error) {
	select {
	case <-ctx.Done():
		return RenderQueueJob{ID: id, State: "running"}, ctx.Err()
	case <-time.After(s.terminalAfter):
		return RenderQueueJob{ID: id, State: RenderQueueStateCompleted}, nil
	}
}

func TestBoundedWaitReportsPendingInsteadOfHoldingTheLane(t *testing.T) {
	stub := newRenderQueueStub(time.Hour) // the render outlives the budget by far

	started := time.Now()
	job, _, err := WaitRenderQueueTerminalBounded(context.Background(), stub, "render-1", 5*time.Millisecond, 60*time.Millisecond)
	elapsed := time.Since(started)

	if !errors.Is(err, ErrRenderQueuePending) {
		t.Fatalf("err = %v, want ErrRenderQueuePending", err)
	}
	if job.ID != "render-1" {
		t.Fatalf("job.ID = %q, want the handle preserved so the caller can ask again", job.ID)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("bounded wait took %s: it must end on its budget, not on the render", elapsed)
	}
	if stub.gets == 0 {
		t.Fatal("the polling fallback never asked the queue")
	}
}

func TestBoundedWaitReturnsATerminalRenderInsideTheBudget(t *testing.T) {
	stub := newRenderQueueStub(10 * time.Millisecond)

	job, _, err := WaitRenderQueueTerminalBounded(context.Background(), stub, "render-2", 2*time.Millisecond, 2*time.Second)
	if err != nil {
		t.Fatalf("a render that finishes inside the budget must be reported as terminal: %v", err)
	}
	if job.State != RenderQueueStateCompleted {
		t.Fatalf("state = %q, want COMPLETED", job.State)
	}
}

func TestZeroBudgetKeepsTheBlockingContract(t *testing.T) {
	stub := newRenderQueueStub(10 * time.Millisecond)

	job, _, err := WaitRenderQueueTerminalBounded(context.Background(), stub, "render-3", 2*time.Millisecond, 0)
	if err != nil {
		t.Fatalf("budget 0 must keep the historical blocking wait: %v", err)
	}
	if job.State != RenderQueueStateCompleted {
		t.Fatalf("state = %q, want COMPLETED", job.State)
	}
}

// TestBoundedWaitMapsTheEventDrivenPath: with the long-poll port wired, the wait
// parks server-side and the only thing that ends it is the budget. The result
// must still be the capability-owned sentinel — a transport context error is not
// an answer a caller can classify.
func TestBoundedWaitMapsTheEventDrivenPath(t *testing.T) {
	stub := newRenderQueueStub(time.Hour)
	stub.withWaiter = true

	_, _, err := WaitRenderQueueTerminalBounded(context.Background(), stubbedWaiter{stub}, "render-4", time.Millisecond, 40*time.Millisecond)
	if !errors.Is(err, ErrRenderQueuePending) {
		t.Fatalf("err = %v, want ErrRenderQueuePending (never a raw context error)", err)
	}
}

// TestCallerCancellationIsNotAPendingWait: a cancelled run must not look like a
// render that is still coming.
func TestCallerCancellationIsNotAPendingWait(t *testing.T) {
	stub := newRenderQueueStub(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := WaitRenderQueueTerminalBounded(ctx, stub, "render-5", time.Millisecond, time.Minute)
	if err == nil {
		t.Fatal("a cancelled caller must get an error")
	}
	if errors.Is(err, ErrRenderQueuePending) {
		t.Fatalf("err = %v: caller cancellation must not be reported as a pending render", err)
	}
}

func TestBoundedWaitRequiresAClient(t *testing.T) {
	if _, _, err := WaitRenderQueueTerminalBounded(context.Background(), nil, "render-6", time.Millisecond, time.Second); err == nil {
		t.Fatal("a nil client must be refused, not waited on")
	}
}

// stubbedWaiter adds the event-driven port to the stub. The interface is
// asserted here (not on the stub itself) so the type-switch in
// WaitRenderQueueTerminal sees exactly one capability.
type stubbedWaiter struct{ *renderQueueStub }

func (s stubbedWaiter) WaitTerminal(ctx context.Context, id string) (RenderQueueJob, error) {
	return s.renderQueueStub.waitTerminal(ctx, id)
}

// The two clients must stay distinguishable: the polling one must NOT satisfy
// the event-driven port, or every bounded-wait test would silently exercise the
// long-poll path instead of the fallback it claims to cover.
var (
	_ RenderQueueClient = (*renderQueueStub)(nil)
	_ RenderQueueWaiter = stubbedWaiter{}
)
