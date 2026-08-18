package localization

// scheduler.go owns the canonical parallel scheduler for localized renders:
// a bounded worker pool that runs at most `render_concurrency` renders at once
// while preserving the requested priority order. It is the queue/worker
// surface from the plan:
//
//	EN plan (priority 0) ──┐
//	ES plan (priority 1) ──┼── bounded worker pool ──→ []TaskResult
//	IT plan (priority 2) ──┘   (render_concurrency)
//
// godlike/06 SSOT (one canonical owner per fact): this is the SINGLE scheduler
// for the localization fan-out. Callers stream tasks in priority order as each
// language's plan becomes ready — a language starts rendering as soon as a
// worker slot is free, with no global "translate-all + plan-all" barrier. The
// scheduler returns results in submission (priority) order, never completion
// order, so a lower-priority language that finishes first cannot reorder the
// report.

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"
)

// RenderFunc executes one localized plan into a certified artifact. It is the
// seam where the compiler (LocalizedClipPlan → RenderPlan) and the Rust
// executor wire in — the scheduler never knows HOW a plan renders, only that
// it renders with bounded concurrency.
type RenderFunc func(ctx context.Context, plan LocalizedClipPlan) (LocalizedClipArtifact, error)

// LocalizedClipTask is one queued render unit.
//
// Priority is the deterministic order (source=0, targets=1..N). The scheduler
// stores results in submission order, so callers submit tasks in priority
// order and get a deterministic report back. Equal priorities keep their
// submission order (stable).
type LocalizedClipTask struct {
	Priority int
	Plan     LocalizedClipPlan
}

// TaskResult is one completed render's outcome, keyed by its submission slot.
// Artifact is zero when Err is non-nil (the render failed or was cancelled).
type TaskResult struct {
	Priority int
	Artifact LocalizedClipArtifact
	Err      error
}

// Scheduler is a bounded worker pool for localized renders. It is immutable
// after construction; Submit streams tasks in and Wait collects the ordered
// results.
type Scheduler struct {
	render      RenderFunc
	g           *errgroup.Group
	ctx         context.Context
	concurrency int

	mu      sync.Mutex
	results []TaskResult
}

// NewScheduler builds a bounded worker pool. render is mandatory (fail-closed:
// a pool with no render function can never produce an artifact); concurrency
// < 1 is clamped to 1.
func NewScheduler(ctx context.Context, render RenderFunc, concurrency int) (*Scheduler, error) {
	if render == nil {
		return nil, fmt.Errorf("localization.NewScheduler: render func is required")
	}
	if concurrency < 1 {
		concurrency = 1
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)
	return &Scheduler{render: render, g: g, ctx: gctx, concurrency: concurrency}, nil
}

// Concurrency returns the configured render_concurrency.
func (s *Scheduler) Concurrency() int {
	if s == nil {
		return 0
	}
	return s.concurrency
}

// Submit enqueues one task. It never blocks on a worker: the task starts as
// soon as a worker slot is free, so a language renders as soon as its plan is
// ready instead of waiting behind a global barrier. Returns the submission
// index (the task's priority slot).
//
// Submit must be called before Wait; callers stream submissions sequentially
// in priority order (the canonical fan-out order).
func (s *Scheduler) Submit(task LocalizedClipTask) int {
	s.mu.Lock()
	idx := len(s.results)
	s.results = append(s.results, TaskResult{Priority: task.Priority})
	s.mu.Unlock()

	s.g.Go(func() error {
		artifact, err := s.render(s.ctx, task.Plan)
		s.mu.Lock()
		s.results[idx] = TaskResult{Priority: task.Priority, Artifact: artifact, Err: err}
		s.mu.Unlock()
		// Per-task errors never abort the pool: one failed language is
		// recorded on its result, and the remaining languages still render.
		return nil
	})
	return idx
}

// Wait blocks until every submitted task completes and returns results in
// submission (priority) order — deterministic, never completion order.
func (s *Scheduler) Wait() []TaskResult {
	if s == nil {
		return nil
	}
	_ = s.g.Wait()
	s.mu.Lock()
	results := make([]TaskResult, len(s.results))
	copy(results, s.results)
	s.mu.Unlock()
	return results
}
