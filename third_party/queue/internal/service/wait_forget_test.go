package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository/memory"
)

// parkedWaiters reports how many channels are currently parked in the
// notifier's FIFO. A waiter that returns without Forget leaves its channel
// behind, where the next single Notify consumes it instead of waking a live
// caller. The test package can inspect the queue under the notifier's own
// mutex; production code has no need for this view.
func parkedWaiters(n *Notifier) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.queue)
}

func waitUntilParked(t *testing.T, n *Notifier, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if parkedWaiters(n) == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("notifier parked waiters = %d, want %d", parkedWaiters(n), want)
}

// TestWaitStateForgetsWaiterOnTimeout pins the deadline branch: a producer
// whose wait window elapsed is no longer waiting, so its channel must leave
// the single-wake FIFO.
func TestWaitStateForgetsWaiterOnTimeout(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	ctx := context.Background()
	if err := svc.Submit(ctx, model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}

	job, err := svc.WaitState(ctx, "job-1", 80*time.Millisecond)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if job == nil || job.State != model.StatePending {
		t.Fatalf("got %+v, want the current pending job", job)
	}
	if got := parkedWaiters(svc.notify); got != 0 {
		t.Fatalf("%d parked waiter(s) left after timeout; an abandoned waiter would consume the next single wake", got)
	}
}

// TestWaitStateForgetsWaiterOnCancellation pins the context branch.
func TestWaitStateForgetsWaiterOnCancellation(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	if err := svc.Submit(context.Background(), model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := svc.WaitState(ctx, "job-1", 10*time.Second)
		done <- err
	}()
	waitUntilParked(t, svc.notify, 1)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("wait error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitState did not return after cancellation")
	}
	if got := parkedWaiters(svc.notify); got != 0 {
		t.Fatalf("%d parked waiter(s) left after cancellation", got)
	}
}

// TestWaitStateForgetsReplacementWaiterAfterWake pins the subtler leak: on a
// wake the waiter parks a NEW channel before re-reading the store. If the read
// is terminal, the replacement must be forgotten too; a deferred call whose
// argument was the ORIGINAL channel would miss it.
func TestWaitStateForgetsReplacementWaiterAfterWake(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	ctx := context.Background()
	if err := svc.Submit(ctx, model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Claim(ctx, "w1"); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := svc.WaitState(ctx, "job-1", 10*time.Second)
		done <- err
	}()
	waitUntilParked(t, svc.notify, 1)
	if err := svc.Complete(ctx, "job-1", "w1", 1, model.Artifact{
		StorageKey: "abc", ArtifactHash: "abc", SizeBytes: 1, ContentType: "video/mp4", FrameCount: 1, DurationUS: 1,
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("wait: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitState did not return after completion")
	}
	if got := parkedWaiters(svc.notify); got != 0 {
		t.Fatalf("%d parked waiter(s) left after the terminal wake", got)
	}
}

// TestWaitStateConcurrentWaitersLeaveNoParkedChannels exercises Done/Forget from
// many goroutines under the race detector: every waiter times out on the same
// pending job, and the FIFO must be empty at the end.
func TestWaitStateConcurrentWaitersLeaveNoParkedChannels(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	ctx := context.Background()
	if err := svc.Submit(ctx, model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}

	const waiters = 6
	var wg sync.WaitGroup
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.WaitState(ctx, "job-1", 120*time.Millisecond); err != nil {
				t.Errorf("wait: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := parkedWaiters(svc.notify); got != 0 {
		t.Fatalf("%d parked waiter(s) left after %d concurrent waits", got, waiters)
	}
}
