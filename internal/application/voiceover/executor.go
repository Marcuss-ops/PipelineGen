// Package voiceover — executor.go (PR-VOICEOVER-BOUNDED-EXECUTOR, Blocco 3, June 2026).
//
// Implements the bounded parallel executor for the per-language fan-out.
// Strategy (per AGENTS.md Pattern 0 + thinker Q1-Q9):
//
//   - Executor.Run consumes []Task from planner.go.
//   - Output ordering is strictly input-mapped (results[t.Index] = ...).
//   - Per-task panic isolation: goroutine handle via recover() returns
//     a StatusFailed result with the panic message; concurrent tasks
//     continue unaffected.
//   - Context cancellation: Option (c) — when ctx is cancelled, new
//     tasks are NOT started (they return a "context canceled" failed
//     result immediately) and in-flight tasks are NOT cancelled
//     (run to completion); WaitGroup waits cleanly for in-flight
//     tasks to finish.
//   - Bounded concurrency via a semaphore-cap chan. cap ==
//     EffectiveParallelism(requested, max, taskCount) computed by the
//     orchestrator (Execute) before delegating to Run.
//   - Per-task progress callback ProgressFunc (nil-safe).
//
// Why a custom worker pool vs pkg/concurrent.Map: the canonical helper
// aborts on first error (errgroup-style); we need per-task failure
// isolation so five failing languages don't kill the third succeeding
// language's Slot acquisition. Option (c) cancellation + bounded
// semaphore is the exact semantics the user's pasted plan asks for.
package voiceover

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"

	"go.uber.org/zap"
)

// EffectiveParallelism clamps the requested parallelism to the
// canonical min over (max, taskCount). The use case Execute is
// responsible for substituting DefaultParallelism when requested==0
// BEFORE calling this helper — EffectiveParallelism is pure clamp.
//
// Reasons for preferring a pure clamp at the executor layer:
//   - db 1+2: single responsibility; the function name advertises
//     "compute the runtime cap".
//   - new default substitution: orchestration concerns (when to fall
//     back) live in Execute, not buried in the helper.
//
// Negative input is treated as 0 (zero parallelism → usage is
// nonsensical; the caller should pre-substitute DefaultParallelism).
// Zero or negative taskCount returns 0 so the caller can short-circuit
// before allocating the semaphore channel.
func EffectiveParallelism(requested, maxP, taskCount int) int {
	if taskCount <= 0 {
		return 0
	}
	if requested < 1 {
		return 0
	}
	if requested > maxP {
		requested = maxP
	}
	if requested > taskCount {
		requested = taskCount
	}
	return requested
}

// Executor runs Tasks with bounded concurrency. Stateless except for
// the logger; Runner is concurrency-safe and re-usable across calls.
type Executor struct {
	logger *zap.Logger
}

// NewExecutor constructs an Executor. logger is optional (nil-safe via
// zap.NewNop()).
func NewExecutor(logger *zap.Logger) *Executor {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Executor{logger: logger}
}

// Run executes tasks concurrently with cap==concurrency. Output ordering
// matches input tasks indexing strictly (results[i] corresponds to
// tasks[i]). Per-task panic isolation: a panic in one task becomes a
// StatusFailed result with the panic message + stack; concurrent tasks
// continue unaffected. Context cancellation propagates via Option (c):
//
//   - Before goroutine launch: if ctx is cancelled, the task is
//     recorded as StatusFailed + "context canceled before start" and
//     no worker is spawned for it.
//   - After goroutine completion: if ctx was cancelled, the result is
//     flagged StatusFailed (even if individual TaskFn succeeded) so
//     per-item status reflects the cancellation cleanly.
//
// fn is REQUIRED — Run returns an error if fn is nil so a missing
// composition-root wire-up fails loudly instead of silently no-op'ing
// (godlike/07 "no fake availability"). prog is nil-safe.
func (e *Executor) Run(
	ctx context.Context,
	tasks []Task,
	concurrency int,
	fn TaskFn,
	prog ProgressFunc,
) ([]TaskResult, error) {
	if fn == nil {
		return nil, fmt.Errorf("Executor.Run: TaskFn is nil (composition root did not bind the per-task worker)")
	}
	if len(tasks) == 0 {
		return []TaskResult{}, nil
	}
	if concurrency < 1 {
		// Defensive: caller should have computed EffectiveParallelism
		// already, but if they pass 0 we short-circuit with
		// sequential execution so the call never deadlocks.
		concurrency = 1
	}

	out := make([]TaskResult, len(tasks))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, t := range tasks {
		// Cancellation fast-path BEFORE acquiring slot.
		if ctx.Err() != nil {
			out[i] = TaskResult{
				Language: t.Language,
				Status:   StatusFailed,
				Error:    "context canceled before start",
				ID:       t.ID,
				Filename: t.Filename,
			}
			// PR-VO-TYPED-PRIMITIVES (July 2026): the prog callback
			// (if non-nil) receives the typed Language verbatim.
			// No string conversion needed at the TaskResult literal.
			if prog != nil {
				prog(ctx, out[i])
			}
			continue
		}

		// Acquire semaphore slot via select+ctx.Done() so ctx
		// cancellation between the pre-launch check above and the
		// slot acquire propagates cleanly (no deadlock on a
		// blocking channel send). The pre-launch check is kept as
		// an optimization for the common case; this select is the
		// safe fallback for the race window.
		select {
		case sem <- struct{}{}:
			// proceed to spawn
		case <-ctx.Done():
			out[i] = TaskResult{
				Language: t.Language,
				Status:   StatusFailed,
				Error:    "context canceled before slot",
				ID:       t.ID,
				Filename: t.Filename,
			}
			if prog != nil {
				prog(ctx, out[i])
			}
			continue
		}
		wg.Add(1)
		go func(idx int, task Task) {
			defer wg.Done()
			defer func() { <-sem }() // release slot at exit

			// Cancellation fast-path inside goroutine (covers the
			// window between Slot acquisition and worker start).
			if ctx.Err() != nil {
				out[idx] = TaskResult{
					Language: task.Language,
					Status:   StatusFailed,
					Error:    "context canceled before fn invocation",
					ID:       task.ID,
					Filename: task.Filename,
				}
				if prog != nil {
					prog(ctx, out[idx])
				}
				return
			}

			res := e.runTaskFnWithRecover(ctx, task, fn)

			// Post-completion cancellation marker: if the parent
			// ctx was cancelled while this task ran, surface it on
			// the result so per-item Status reflects the cancelled
			// state (callers should not interpret a "completed"
			// result as "the system was healthy" when ctx is
			// cancelled — the per-item status must read "failed by
			// cancellation" for audit).
			if ctx.Err() != nil && res.Status == StatusCompleted {
				res.Status = StatusFailed
				if res.Error == "" {
					res.Error = "context canceled after fn returned"
				}
			}

			out[idx] = res
			if prog != nil {
				prog(ctx, res)
			}
		}(i, t)
	}

	wg.Wait()
	return out, nil
}

// runTaskFnWithRecover invokes fn(ctx, task) with panic isolation.
// On panic, returns TaskResult.StatusFailed with the panic message +
// the stack trace is logged via e.logger (operator grep surface).
func (e *Executor) runTaskFnWithRecover(ctx context.Context, task Task, fn TaskFn) (r TaskResult) {
	defer func() {
		if rec := recover(); rec != nil {
			if e.logger != nil {
				// PR-VO-TYPED-PRIMITIVES (July 2026): task.Language
				// is the typed Language envelope; zap.String requires
				// a string argument. Convert at the log-seam.
				e.logger.Error("Executor: task panicked (recovered)",
					zap.String("language", string(task.Language)),
					zap.Int("index", task.Index),
					zap.String("id", task.ID),
					zap.Any("panic", rec),
					zap.String("stack", string(debug.Stack())))
			}
			r = TaskResult{
				Language: task.Language,
				Status:   StatusFailed,
				Error:    fmt.Sprintf("panic: %v", rec),
				ID:       task.ID,
				Filename: task.Filename,
			}
		}
	}()
	return fn(ctx, task)
}
