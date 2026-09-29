package concurrent

import (
	"context"
	"testing"
	"time"
)

// TestFairSemaphore_EnforcesCapacity pins the capacity contract: the (cap+1)-th
// acquirer blocks until a slot is released.
func TestFairSemaphore_EnforcesCapacity(t *testing.T) {
	sem := NewFairSemaphore(2)
	rel1, err := sem.AcquireCtx(context.Background(), "a")
	if err != nil {
		t.Fatalf("acquire 1: %v", err)
	}
	rel2, err := sem.AcquireCtx(context.Background(), "a")
	if err != nil {
		t.Fatalf("acquire 2: %v", err)
	}
	if sem.InFlight() != 2 {
		t.Fatalf("in-flight = %d, want 2", sem.InFlight())
	}

	done := make(chan struct{})
	go func() {
		rel3, err := sem.AcquireCtx(context.Background(), "b")
		if err != nil {
			t.Errorf("acquire 3: %v", err)
		}
		rel3()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("third acquirer was admitted while the semaphore was full")
	case <-time.After(50 * time.Millisecond):
	}

	rel1()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("third acquirer was never admitted after a release")
	}
	rel2()
	if sem.InFlight() != 0 {
		t.Fatalf("in-flight after releases = %d, want 0", sem.InFlight())
	}
}

// TestFairSemaphore_StarvingOwnerBeatsOwnerWithSlots is the headline fairness
// contract from the 2026-09-28 measurement: when a slot frees, a starving owner
// is served BEFORE the same releasing owner's next queued request. Without this
// rule a job pipelining 20 Drive uploads starves another job's single upload for
// minutes (measured: 85.7 s of queue wait charged as upload work).
func TestFairSemaphore_StarvingOwnerBeatsOwnerWithSlots(t *testing.T) {
	sem := NewFairSemaphore(2)
	relA1, err := sem.AcquireCtx(context.Background(), "job-a")
	if err != nil {
		t.Fatalf("acquire a1: %v", err)
	}
	relA2, err := sem.AcquireCtx(context.Background(), "job-a")
	if err != nil {
		t.Fatalf("acquire a2: %v", err)
	}
	if sem.InFlight() != 2 {
		t.Fatalf("in-flight = %d, want 2", sem.InFlight())
	}

	// job-a pipelines one more request (queued first), then job-b queues behind
	// it. Both are blocked: the semaphore is full.
	var relA3 func()
	a3Done := make(chan struct{})
	go func() {
		relA3, err = sem.AcquireCtx(context.Background(), "job-a")
		if err != nil {
			t.Errorf("acquire a3: %v", err)
		}
		close(a3Done)
	}()
	for sem.Waiting() != 1 {
		time.Sleep(time.Millisecond)
	}
	gotB := make(chan func(), 1)
	go func() {
		rel, err := sem.AcquireCtx(context.Background(), "job-b")
		if err != nil {
			t.Errorf("acquire b: %v", err)
			return
		}
		gotB <- rel
	}()
	for sem.Waiting() != 2 {
		time.Sleep(time.Millisecond)
	}

	// Releasing a slot must serve job-b (starving, different owner), not the
	// same owner's queued request that sits AHEAD of it in FIFO order.
	relA1()
	var relB func()
	select {
	case relB = <-gotB:
	case <-time.After(time.Second):
		t.Fatal("job-b was never admitted after job-a released a slot")
	}
	select {
	case <-a3Done:
		t.Fatal("job-a's queued request was admitted while job-b was starving")
	default:
	}

	// Only after job-b releases does job-a's next request run.
	relB()
	select {
	case <-a3Done:
	case <-time.After(time.Second):
		t.Fatal("job-a's queued request was never admitted after job-b released")
	}
	relA3()
	relA2()
	if sem.InFlight() != 0 || sem.Waiting() != 0 {
		t.Fatalf("leaked state: in-flight=%d waiting=%d", sem.InFlight(), sem.Waiting())
	}
}

// TestFairSemaphore_FIFOWithinOneOwner pins that fairness does not reorder work
// belonging to the same owner.
func TestFairSemaphore_FIFOWithinOneOwner(t *testing.T) {
	sem := NewFairSemaphore(1)
	rel, err := sem.AcquireCtx(context.Background(), "job-a")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	order := make(chan int, 3)
	for i := 0; i < 3; i++ {
		idx := i
		go func() {
			relI, err := sem.AcquireCtx(context.Background(), "job-a")
			if err != nil {
				t.Errorf("acquire %d: %v", idx, err)
				return
			}
			order <- idx
			relI()
		}()
		// Keep submit order deterministic relative to the queue.
		for sem.Waiting() != idx+1 {
			time.Sleep(time.Millisecond)
		}
	}
	rel()
	for want := 0; want < 3; want++ {
		select {
		case got := <-order:
			if got != want {
				t.Fatalf("admission order = %d, want %d (FIFO within owner)", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("waiter %d was never admitted", want)
		}
	}
}

// TestFairSemaphore_CancelReleasesNothing pins that a cancelled waiter leaves no
// slot behind: instrumentation must never change availability.
func TestFairSemaphore_CancelReleasesNothing(t *testing.T) {
	sem := NewFairSemaphore(1)
	rel, err := sem.AcquireCtx(context.Background(), "job-a")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := sem.AcquireCtx(ctx, "job-b")
		errCh <- err
	}()
	for sem.Waiting() == 0 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-errCh; err != context.Canceled {
		t.Fatalf("cancelled acquire error = %v, want context.Canceled", err)
	}
	if sem.Waiting() != 0 {
		t.Fatalf("waiting after cancel = %d, want 0", sem.Waiting())
	}
	rel()

	// The slot must be immediately available to a fresh acquirer.
	rel2, err := sem.AcquireCtx(context.Background(), "job-c")
	if err != nil {
		t.Fatalf("acquire after cancel: %v", err)
	}
	rel2()
	if sem.InFlight() != 0 {
		t.Fatalf("in-flight = %d, want 0", sem.InFlight())
	}
}

// TestFairSemaphore_NilSafe pins the nil-receiver contract used by optional
// wiring paths: a nil gate is an unbounded slot, not a panic.
func TestFairSemaphore_NilSafe(t *testing.T) {
	var sem *FairSemaphore
	rel, err := sem.AcquireCtx(context.Background(), "job-a")
	if err != nil {
		t.Fatalf("nil acquire: %v", err)
	}
	rel()
	rel() // double release on a nil gate must not panic
	if sem.InFlight() != 0 || sem.Waiting() != 0 || sem.Cap() != 0 {
		t.Fatalf("nil gate reports state: in-flight=%d waiting=%d cap=%d", sem.InFlight(), sem.Waiting(), sem.Cap())
	}
}

// TestFairSemaphore_RejectsMisconfiguredCapacity pins the fail-fast constructor.
func TestFairSemaphore_RejectsMisconfiguredCapacity(t *testing.T) {
	for _, max := range []int{0, -1} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("NewFairSemaphore(%d) did not panic", max)
				}
			}()
			NewFairSemaphore(max)
		}()
	}
}
