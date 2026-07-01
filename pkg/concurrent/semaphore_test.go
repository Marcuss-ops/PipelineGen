// Package concurrent_test — semaphore_test.go (June 2026, Commit G)
//
// Unit tests for the Semaphore primitive. Pin:
//  - cap/holder contract (tryAcquire returns true up to Cap, then
//    false; Acquire blocks past Cap; Release returns a slot)
//  - panic on zero/negative config (fail-fast on misuse)
//  - AcquireCtx cancellation contract.

package concurrent_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

func TestNewSemaphore_RejectsZeroMax(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("NewSemaphore(0) did not panic; expected fail-fast on zero")
		}
	}()
	_ = concurrent.NewSemaphore(0)
}

func TestNewSemaphore_RejectsNegativeMax(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("NewSemaphore(-1) did not panic; expected fail-fast on negative")
		}
	}()
	_ = concurrent.NewSemaphore(-1)
}

func TestSemaphore_Cap(t *testing.T) {
	s := concurrent.NewSemaphore(3)
	if got := s.Cap(); got != 3 {
		t.Errorf("Cap() = %d, want 3", got)
	}
}

func TestSemaphore_TryAcquireUntilCap(t *testing.T) {
	s := concurrent.NewSemaphore(2)
	if !s.TryAcquire() {
		t.Fatal("first TryAcquire must succeed")
	}
	if !s.TryAcquire() {
		t.Fatal("second TryAcquire must succeed (Cap=2)")
	}
	if s.TryAcquire() {
		t.Fatal("third TryAcquire must fail (Cap=2, both slots held)")
	}
	s.Release()
	if !s.TryAcquire() {
		t.Fatal("TryAcquire after Release must succeed (slot reopened)")
	}
	s.Release()
	if !s.TryAcquire() {
		t.Fatal("TryAcquire after second Release must succeed")
	}
}

func TestSemaphore_AcquireReleaseRoundTrip(t *testing.T) {
	s := concurrent.NewSemaphore(1)
	s.Acquire()
	done := make(chan struct{})
	go func() {
		s.Acquire() // this blocks until Release below
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("Acquire unblocked before Release; semaphore contract broken")
	case <-time.After(50 * time.Millisecond):
		// expected: blocked
	}
	s.Release()
	select {
	case <-done:
		// expected: unblocks after Release
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Acquire didn't unblock after Release within 500ms")
	}
	s.Release()
}

func TestSemaphore_AcquireCtxCancellation(t *testing.T) {
	s := concurrent.NewSemaphore(1)
	s.Acquire() // exhaust the only slot
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel before AcquireCtx
	err := s.AcquireCtx(ctx)
	if err == nil {
		t.Fatal("AcquireCtx returned nil err on cancelled context; want non-nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("AcquireCtx err = %v, want context.Canceled", err)
	}
	s.Release()
}

func TestSemaphore_ConcurrentStress(t *testing.T) {
	const (
		cap          = 4
		totalHolders = 200
	)
	s := concurrent.NewSemaphore(cap)
	concurrentHolders := &atomicInt32{}
	maxConcurrent := &atomicInt32{}
	var wg sync.WaitGroup
	for i := 0; i < totalHolders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Acquire()
			defer s.Release()
			h := concurrentHolders.Add(1)
			for {
				m := maxConcurrent.Load()
				if h <= m || maxConcurrent.CompareAndSwap(m, h) {
					break
				}
			}
			// Simulate work that takes long enough that
			// concurrent holders exceed cap if the semaphore
			// contract is broken.
			time.Sleep(2 * time.Millisecond)
			concurrentHolders.Add(-1)
		}()
	}
	wg.Wait()
	if got := maxConcurrent.Load(); got > int32(cap) {
		t.Errorf("max concurrent holders = %d, want <= %d", got, cap)
	}
	if got := maxConcurrent.Load(); got < 1 {
		t.Errorf("max concurrent holders = %d, want >= 1 (sanity)", got)
	}
}

// atomicInt32 is a tiny wrapper so the test file doesn't have to
// import sync/atomic at the package level (keeps the import graph
// focused). Implementation mirrors stdlib atomic.Int32 with the
// methods we actually need.
type atomicInt32 struct {
	mu sync.Mutex
	v  int32
}

func (a *atomicInt32) Add(delta int32) int32 {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.v += delta
	return a.v
}

func (a *atomicInt32) Load() int32 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.v
}

func (a *atomicInt32) CompareAndSwap(old, new int32) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.v != old {
		return false
	}
	a.v = new
	return true
}
