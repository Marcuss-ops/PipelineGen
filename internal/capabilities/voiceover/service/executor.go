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

// runTaskFnWithRecover invokes fn(ctx, task) with panic isolation.
// On panic, returns TaskResult.StatusFailed with the panic message +
// the stack trace is logged via e.logger (operator grep surface).
