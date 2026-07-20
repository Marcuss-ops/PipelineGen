// Package scripts — semaphore_usecase is the use case for the
// concurrent-script-generation slot manager.
//
// Wave 14 problem #4 (June 2026): previously this logic lived inline
// in api/script/handler_generate_handler.go::HandlerGenerate.Generate
// as:
//
//	select {
//	case h.scriptGenSem <- struct{}{}:
//	    defer func() { <-h.scriptGenSem }()
//	case <-ctx.Done():
//	    return nil, ctx.Err()
//	}
//
// Three problems with the inline form:
//   - the channel cap value (maxScriptGen) was hidden in the handler
//     ctor's local variable, never inspected by the unit tests;
//   - cancellation, acquire, release are tangled with the rest of the
//     handler body, so a noisy change to the handler (instrumentation
//     or stage log insertion) could silently change the slot behaviour;
//   - acquired/released counters do not exist; operators have no
//     observability on the slot pool.
//
// Moving the slot here makes the acquire/release cycle a typed method
// with a release closure, ensures the channel cap is the only knob,
// and adds atomic counters for observability.
//
// The use case owns:
//   - the buffered channel (`sem`) used as the slot pool
//   - acquire semantics: blocked select with ctx cancellation
//   - release semantics: a closure the caller defers
//   - observability counters (AcquireCount / ReleaseCount)
//
// The use case does NOT own:
//   - the job ID or the script payload (caller responsibility)
//   - which downstream execution follows on success (handler /
//     PipelineUseCase responsibility)
//   - the ctx that bounds the acquire (caller responsibility)
package usecase

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"go.uber.org/zap"
)

// ErrSemaphoreAcquisitionCanceled is the sentinel for "the ctx passed
// to Acquire was canceled before a slot became available". The
// handler maps this to a typed error chain; callers free to use
// errors.Is for status-code mapping.
var ErrSemaphoreAcquisitionCanceled = errors.New("semaphore: acquisition canceled")

// ErrSemaphoreMisconfigured is the sentinel for "the use case was
// constructed with capacity <= 0". A new semaphore must have
// capacity >= 1; the ctor refuses zero/negative values.
var ErrSemaphoreMisconfigured = errors.New("semaphore: capacity must be >= 1")

// SemaphoreUseCase is the orchestrator for the script-gen slot pool.
type SemaphoreUseCase struct {
	capacity int
	sem      chan struct{}
	log      *zap.Logger

	acquireCount atomic.Int64
	releaseCount atomic.Int64
}

// NewSemaphoreUseCase constructs the slot pool with the given
// capacity (max concurrent script generations). Capacity <= 0 yields
// ErrSemaphoreMisconfigured; a non-nil log is recommended for ops
// dashboards, but the use case is nil-safe.
func NewSemaphoreUseCase(capacity int, log *zap.Logger) (*SemaphoreUseCase, error) {
	if capacity <= 0 {
		return nil, ErrSemaphoreMisconfigured
	}
	return &SemaphoreUseCase{
		capacity: capacity,
		sem:      make(chan struct{}, capacity),
		log:      log,
	}, nil
}

// Capacity returns the configured slot pool size. Stable accessor
// for tests that want to assert "registry wired capacity correctly".
func (s *SemaphoreUseCase) Capacity() int {
	if s == nil {
		return 0
	}
	return s.capacity
}

// Acquire waits for a free slot from the pool. Returns a release
// closure that the caller MUST defer. On ctx cancellation returns
// ErrSemaphoreAcquisitionCanceled without consuming a slot.
//
// The release closure is safe to call multiple times (subsequent calls
// are no-ops, thanks to a sync.Once semantic via atomic CAS) —
// useful when callers wrap the slot in a helper that may double-release
// by accident.
func (s *SemaphoreUseCase) Acquire(ctx context.Context, jobID string) (release func(), err error) {
	if s == nil {
		return func() {}, fmt.Errorf("%w: semaphore not constructed", ErrSemaphoreMisconfigured)
	}
	if err := ctx.Err(); err != nil {
		return func() {}, fmt.Errorf("%w: pre-check: %w", ErrSemaphoreAcquisitionCanceled, err)
	}

	if s.log != nil {
		s.log.Info("waiting for script generation slot",
			zap.String("job_id", jobID),
			zap.Int("max_concurrent", s.capacity))
	}

	select {
	case s.sem <- struct{}{}:
		if s.log != nil {
			s.log.Info("acquired script generation slot",
				zap.String("job_id", jobID))
		}
		s.acquireCount.Add(1)
		var released atomic.Bool
		return func() {
			if released.CompareAndSwap(false, true) {
				<-s.sem
				s.releaseCount.Add(1)
				if s.log != nil {
					s.log.Info("released script generation slot",
						zap.String("job_id", jobID))
				}
			}
		}, nil
	case <-ctx.Done():
		return func() {}, fmt.Errorf("%w: %w", ErrSemaphoreAcquisitionCanceled, ctx.Err())
	}
}

// AcquireCount returns the cumulative number of successful acquire
// operations. Useful for ops dashboards; tests pin behaviour like
// "after 3 Acquire + Release cycles the counter is 3".
func (s *SemaphoreUseCase) AcquireCount() int64 {
	if s == nil {
		return 0
	}
	return s.acquireCount.Load()
}

// ReleaseCount returns the cumulative number of successful release
// operations. Always <= AcquireCount.
func (s *SemaphoreUseCase) ReleaseCount() int64 {
	if s == nil {
		return 0
	}
	return s.releaseCount.Load()
}
