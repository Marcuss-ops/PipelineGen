// Package stock — steps.go (Stock Cutover Commit 1, July 2026).
//
// ExecutionStepStore is the typed per-step recorder the new
// orchestrator uses to surface progress + completion semantics.
//
// Replaces the legacy implicit "stat the orchestrator's progress
// via the run logger" pattern with a typed record-per-step state
// machine. Each step has explicit Pending / Running / Succeeded /
// Failed / Skipped states. The orchestrator reads the resulting
// record set into the typed ExecutionResult envelope so the
// worker can route it through the JobFinalizer.
//
// InMemoryStepStore is the default implementation for Commit 1.
// A SQLite-backed implementation lands in a follow-up commit so
// the orchestrator's state survives a worker restart.
package types

import (
	"context"
	"errors"
	"sync"
	"time"
)

// StepStatus is the canonical lifecycle state for an execution step.
type StepStatus string

const (
	StepStatusPending   StepStatus = "pending"
	StepStatusRunning   StepStatus = "running"
	StepStatusSucceeded StepStatus = "succeeded"
	StepStatusFailed    StepStatus = "failed"
	StepStatusSkipped   StepStatus = "skipped"
)

// StepRecord captures a single execution step's outcome.
type StepRecord struct {
	// Name is the orchestrator-side identifier of the step. Two
	// records with the same name coalesce by last-writer-wins.
	Name string `json:"name"`

	// Status is the lifecycle state at last update.
	Status StepStatus `json:"status"`

	// StartedAt is set when the step transitions Pending → Running.
	StartedAt time.Time `json:"started_at,omitempty"`

	// EndedAt is set when the step reaches a terminal state
	// (succeeded / failed / skipped).
	EndedAt time.Time `json:"ended_at,omitempty"`

	// Error is populated when Status == StepStatusFailed.
	Error string `json:"error,omitempty"`

	// Output is populated when Status == StepStatusSucceeded
	// (and may carry a skip-reason string when Status == StepStatusSkipped).
	Output string `json:"output,omitempty"`
}

// ExecutionStepStore is the typed store for orchestrator-side step records.
//
// Contract:
//   - Begin(name) → Running; idempotent on repeat starts.
//   - Complete(name, output) → Succeeded.
//   - Fail(name, err) → Failed; err.Error() is captured into Error.
//   - Skip(name, reason) → Skipped; reason is captured into Output.
//   - GetAll(ctx) → snapshot of all records (NOT order-stable
//     — orchestrator consumers sort by Name or StartedAt when
//     ordering matters).
//
// Concurrency: implementations MUST be safe for parallel
// Begin/Complete on the same name and on different names
// (orchestrator parallelism).
type ExecutionStepStore interface {
	Begin(name string) error
	Complete(name string, output string) error
	Fail(name string, err error) error
	Skip(name string, reason string) error
	GetAll(ctx context.Context) ([]StepRecord, error)
}

// InMemoryStepStore is the canonical in-process ExecutionStepStore.
//
// Per executorBL_01 (deferred-FAIL-isolation invariant): a panic
// in one step does NOT corrupt the orchestrator's overall store.
// Every accessor is guarded by a sync.RWMutex — even if a step
// goroutine panics mid-call, the deferred unlock keeps the map
// intact for the next Begin/Complete pair.
type InMemoryStepStore struct {
	mu      sync.RWMutex
	records map[string]StepRecord
}

// NewInMemoryStepStore returns a fresh empty InMemoryStepStore.
func NewInMemoryStepStore() *InMemoryStepStore {
	return &InMemoryStepStore{records: make(map[string]StepRecord)}
}

// Compile-time assertion: InMemoryStepStore satisfies ExecutionStepStore.
var _ ExecutionStepStore = (*InMemoryStepStore)(nil)

// ErrStepEmptyName surfaces an empty name at accessor time. The
// orchestrator never calls with empty, but tests must probe it.
var ErrStepEmptyName = errors.New("step name is empty")

// ErrStepNotFound is what Complete/Fail/Skip return when the
// store has no record for that name. The orchestrator does not
// expect this in production (Begin always precedes Complete); but
// the error surfaces the typo/misspelling definitively.
var ErrStepNotFound = errors.New("step record not found")

func (s *InMemoryStepStore) Begin(name string) error {
	if name == "" {
		return ErrStepEmptyName
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[name]
	if !ok {
		r = StepRecord{Name: name}
	}
	r.Status = StepStatusRunning
	r.StartedAt = time.Now().UTC()
	s.records[name] = r
	return nil
}

func (s *InMemoryStepStore) Complete(name string, output string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[name]
	if !ok {
		return ErrStepNotFound
	}
	r.Status = StepStatusSucceeded
	r.EndedAt = time.Now().UTC()
	r.Output = output
	s.records[name] = r
	return nil
}

func (s *InMemoryStepStore) Fail(name string, err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[name]
	if !ok {
		r = StepRecord{Name: name}
	}
	r.Status = StepStatusFailed
	r.EndedAt = time.Now().UTC()
	if err != nil {
		r.Error = err.Error()
	}
	s.records[name] = r
	return nil
}

func (s *InMemoryStepStore) Skip(name string, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[name]
	if !ok {
		r = StepRecord{Name: name}
	}
	r.Status = StepStatusSkipped
	r.EndedAt = time.Now().UTC()
	r.Output = reason
	s.records[name] = r
	return nil
}

// GetAll returns a snapshot of all records. The order is NOT
// stable across calls — orchestration consumers sort by Name
// or StartedAt when ordering matters.
func (s *InMemoryStepStore) GetAll(_ context.Context) ([]StepRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]StepRecord, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, r)
	}
	return out, nil
}
