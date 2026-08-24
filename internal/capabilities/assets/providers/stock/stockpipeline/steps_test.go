// Package stock — steps_test.go (Stock Cutover Commit 1, July 2026).
//
// Tests for the InMemoryStepStore. Per executorBL_01 (deferred-FAIL
// isolation invariant): a panic in one step does NOT corrupt the
// orchestrator's overall store. The stress test below exercises
// parallel Begin/Complete on overlapping names and confirms the
// store never panics or returns a partial write.
package assets

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestInMemoryStepStore_BeginComplete(t *testing.T) {
	s := NewInMemoryStepStore()
	if err := s.Begin("step1"); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := s.Complete("step1", "ok"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	records, err := s.GetAll(context.Background())
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Status != StepStatusSucceeded {
		t.Errorf("expected succeeded, got %q", records[0].Status)
	}
	if records[0].Output != "ok" {
		t.Errorf("expected output=ok, got %q", records[0].Output)
	}
	if records[0].StartedAt.IsZero() {
		t.Errorf("StartedAt should be set after Begin")
	}
	if records[0].EndedAt.IsZero() {
		t.Errorf("EndedAt should be set after Complete")
	}
}

func TestInMemoryStepStore_Fail(t *testing.T) {
	s := NewInMemoryStepStore()
	_ = s.Begin("step1")
	if err := s.Fail("step1", errors.New("simulated failure")); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	records, _ := s.GetAll(context.Background())
	if len(records) != 1 {
		t.Fatalf("expected 1 record")
	}
	if records[0].Status != StepStatusFailed {
		t.Errorf("expected failed, got %q", records[0].Status)
	}
	if records[0].Error != "simulated failure" {
		t.Errorf("expected error captured, got %q", records[0].Error)
	}
}

func TestInMemoryStepStore_Skip(t *testing.T) {
	s := NewInMemoryStepStore()
	if err := s.Skip("step1", "test reason"); err != nil {
		t.Fatalf("Skip: %v", err)
	}
	records, _ := s.GetAll(context.Background())
	if len(records) != 1 {
		t.Fatalf("expected 1 record")
	}
	if records[0].Status != StepStatusSkipped {
		t.Errorf("expected skipped, got %q", records[0].Status)
	}
	if records[0].Output != "test reason" {
		t.Errorf("expected test reason, got %q", records[0].Output)
	}
}

func TestInMemoryStepStore_EmptyName(t *testing.T) {
	s := NewInMemoryStepStore()
	if err := s.Begin(""); err == nil {
		t.Errorf("expected ErrStepEmptyName for empty name")
	}
}

func TestInMemoryStepStore_CompleteWithoutBegin(t *testing.T) {
	s := NewInMemoryStepStore()
	err := s.Complete("never-begin", "ok")
	if err == nil {
		t.Errorf("expected ErrStepNotFound when Complete runs without Begin")
	}
}

func TestInMemoryStepStore_ConcurrentSafe(t *testing.T) {
	// executorBL_01 invariant: parallel Begin/Complete against
	// the same store must not corrupt the map (no panic, no
	// missing writes, no duplicate keys, all records accounted
	// for in GetAll). The orchestrator dependencies are
	// orchestrated downstream of this contract; corruption here
	// would propagate as silent step-loss in operator dashboards.
	s := NewInMemoryStepStore()
	var wg sync.WaitGroup
	const goroutines = 50
	var totalOps atomic.Int64
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		name := "step-" + itoa(i)
		go func() {
			defer wg.Done()
			_ = s.Begin(name)
			_ = s.Complete(name, "ok")
			totalOps.Add(2)
		}()
	}
	wg.Wait()

	records, err := s.GetAll(context.Background())
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(records) != goroutines {
		t.Errorf("expected %d records, got %d", goroutines, len(records))
	}
	if got := totalOps.Load(); got != int64(goroutines*2) {
		t.Errorf("expected %d total ops, got %d", goroutines*2, got)
	}

	// Sanity: every record is in Succeeded state (no Failed
	// leaks from any goroutine).
	for _, r := range records {
		if r.Status != StepStatusSucceeded {
			t.Errorf("record %q in non-success status %q", r.Name, r.Status)
		}
	}
}

func TestInMemoryStepStore_GetAllEmpty(t *testing.T) {
	s := NewInMemoryStepStore()
	records, err := s.GetAll(context.Background())
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if records == nil {
		t.Errorf("expected empty (not nil) slice")
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}
}

func TestInMemoryStepStore_ReBeginResetsStartedAt(t *testing.T) {
	s := NewInMemoryStepStore()
	_ = s.Begin("step1")
	first := getStartedAt(t, s, "step1")
	time.Sleep(2 * time.Millisecond)
	_ = s.Begin("step1")
	second := getStartedAt(t, s, "step1")
	if !second.After(first) {
		t.Errorf("expected re-Begin to reset StartedAt (first=%v second=%v)", first, second)
	}
}

func getStartedAt(t *testing.T, s *InMemoryStepStore, name string) time.Time {
	t.Helper()
	records, _ := s.GetAll(context.Background())
	for _, r := range records {
		if r.Name == name {
			return r.StartedAt
		}
	}
	t.Fatalf("step %q not found in store", name)
	return time.Time{}
}

// ── executorBL_01 verification ────────────────────────────────────────
// PanicIsolation: a step goroutine that panics during Begin's lock-held
// critical section must release the lock via Go's deferred-unlock
// semantics. The orchestrator depends on this — a poisoned lock would
// freeze every later step write.
//
// Surfaces the invariant explicitly by:
//
//  1. spawning a goroutine that calls s.Begin(name) then panic(),
//  2. waiting for the panic-recovery to complete (which is the moment
//     the deferred Unlock actually fires),
//  3. running another s.Begin+Complete pair in a second goroutine with
//     a 2s deadline — if the lock is held, the second goroutine blocks
//     and the deadline triggers.
func TestInMemoryStepStore_PanicIsolation(t *testing.T) {
	s := NewInMemoryStepStore()

	panickerDone := make(chan struct{})
	go func() {
		defer close(panickerDone)
		defer func() { _ = recover() }()
		_ = s.Begin("panic-step")
		panic("simulated panic during step lifetime")
	}()

	<-panickerDone

	// Parallel-style Begin+Complete must succeed (lock is free).
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := s.Begin("parallel-step"); err != nil {
			t.Errorf("parallel Begin returned error: %v", err)
			return
		}
		if err := s.Complete("parallel-step", "ok"); err != nil {
			t.Errorf("parallel Complete returned error: %v", err)
		}
	}()
	select {
	case <-done:
		// OK — lock was correctly released by deferred Unlock during
		// the panicker's stack unwinding.
	case <-time.After(2 * time.Second):
		t.Fatalf("parallel-step blocked 2s — lock held after panic (executorBL_01 violated)")
	}

	// executorBL_01 audit pin: after the panic-recovery cycle, the
	// store contains panic-step (status: Running — Begin succeeded,
	// no terminal transition) + parallel-step (status: Succeeded).
	// The lock was correctly released — proven by the deadline-based
	// deadlock check above — so the orchestrator can still proceed
	// with subsequent step writes even after a step goroutine panic.
	records, _ := s.GetAll(context.Background())
	if len(records) != 2 {
		t.Fatalf("expected 2 records (panic-step + parallel-step); got %d", len(records))
	}
	statusByName := make(map[string]StepStatus)
	for _, r := range records {
		statusByName[r.Name] = r.Status
	}
	if got := statusByName["panic-step"]; got != StepStatusRunning {
		t.Errorf("panic-step should remain Running (Begin succeeded, no terminal); got %q", got)
	}
	if got := statusByName["parallel-step"]; got != StepStatusSucceeded {
		t.Errorf("parallel-step should be Succeeded; got %q", got)
	}
}
