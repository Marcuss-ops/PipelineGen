package renderqueue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// RenderingGen queue job states are wire facts owned by the queue.
const (
	RenderQueueStateCompleted = "completed"
	RenderQueueStateFailed    = "failed"
	RenderQueueStateCancelled = "cancelled"
)

// terminalRenderState reports whether state is a terminal render state.
func terminalRenderState(state string) bool {
	switch state {
	case RenderQueueStateCompleted, RenderQueueStateFailed, RenderQueueStateCancelled:
		return true
	default:
		return false
	}
}

// terminalRenderResult is the single classifier for terminal queue states.
func terminalRenderResult(job RenderQueueJob, id string, metrics RenderCompletionMetrics) (RenderQueueJob, RenderCompletionMetrics, error) {
	switch job.State {
	case RenderQueueStateCompleted:
		return job, metrics, nil
	case RenderQueueStateFailed, RenderQueueStateCancelled:
		reason := job.FailReason
		if reason == "" {
			if job.State == RenderQueueStateCancelled {
				reason = "unknown reason"
			} else {
				reason = "unknown failure"
			}
		}
		return job, metrics, fmt.Errorf("render job %s %s: %s", id, job.State, reason)
	default:
		return job, metrics, fmt.Errorf("render job %s reached unrecognised terminal state %q", id, job.State)
	}
}

// WaitRenderQueueTerminal blocks until the queue reports a TERMINAL state for id
// and returns the terminal job together with the wait it cost.
//
// It is the ONE implementation of "wait for a RenderingGen job to finish": the
// overlay enqueue path (QueueRenderEnqueuer.waitForCompletion) and the
// clip.render settle continuation (renderinggen.ClipRenderExecutor.Settle) both
// call it, so the two can neither disagree about what terminal means nor about
// which states count as success — terminalRenderResult is the single classifier.
// Before this function existed, the platform layer carried its own copy of the
// loop with its own jitter and its own clamps, so the same question had two
// answers that could drift independently.
//
// A client exposing RenderQueueWaiter parks server-side on the state transition
// (the queue's GET /jobs/{id}/wait) and spends no client-side polling sleep; a
// client without it (older deployment, test double) falls back to the bounded
// poll loop, whose sleeps and poll count are measured rather than guessed.
// interval <= 0 selects DefaultPollInterval, and only the fallback loop
// uses it.
func WaitRenderQueueTerminal(ctx context.Context, client RenderQueueClient, id string, interval time.Duration) (RenderQueueJob, RenderCompletionMetrics, error) {
	if client == nil {
		return RenderQueueJob{}, RenderCompletionMetrics{}, fmt.Errorf("render queue wait: client is not configured")
	}
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	waitStarted := time.Now()
	metrics := RenderCompletionMetrics{PollInterval: interval}

	// Event-driven completion: the wait parks server-side on the terminal
	// state transition, so the observed completion latency is the transition
	// itself rather than up to one poll interval. No client-side polling sleep
	// is recorded because none is spent.
	if waiter, ok := client.(RenderQueueWaiter); ok {
		job, err := waiter.WaitTerminal(ctx, id)
		metrics.CompletionWait = time.Since(waitStarted)
		if err != nil {
			return RenderQueueJob{}, metrics, err
		}
		return terminalRenderResult(job, id, metrics)
	}

	// Polling fallback for queue clients without the wait capability.
	for {
		job, err := client.Get(ctx, id)
		metrics.PollCount++
		if err != nil {
			return RenderQueueJob{}, metrics, err
		}
		switch {
		case terminalRenderState(job.State):
			metrics.CompletionWait = time.Since(waitStarted)
			return terminalRenderResult(job, id, metrics)
		}

		sleepStarted := time.Now()
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			metrics.PollingSleep += time.Since(sleepStarted)
			metrics.CompletionWait = time.Since(waitStarted)
			return RenderQueueJob{}, metrics, ctx.Err()
		case <-timer.C:
			metrics.PollingSleep += time.Since(sleepStarted)
		}
	}
}

// ErrRenderQueuePending marks a bounded wait that ended because its BUDGET ran
// out while the remote render is still running. It is a WAIT, not a failure:
// the caller can hand its attempt back (job.DeferredAfter — a deferral spends no
// retry budget) and ask again later, which is what keeps a worker lane from
// being parked on a render that may take minutes or hours. A terminal failure or
// a genuine transport fault is NOT this sentinel: those still propagate.
//
// The render handle is returned with it (RenderQueueJob), so the caller never
// has to reconstruct the remote address.
var ErrRenderQueuePending = errors.New("render queue job is not terminal yet")

// ErrRenderQueueStalled marks a render that stopped reporting progress. It is
// DISTINCT from Pending: a pending render is alive and may finish, a stalled one
// holds a GPU lane without advancing. The measured case (2026-11-20) is three
// clip.render children parked 847 s each on a render whose frames never moved.
var ErrRenderQueueStalled = errors.New("render queue job reported no progress")

// watchedProgressSlice is how long the progress watchdog parks on the queue's
// long poll before sampling progress. It is a variable so tests can exercise
// the slice boundary without sleeping 30 s; production never mutates it.
var watchedProgressSlice = 30 * time.Second

// DefaultRenderStallAfter is how long a render may go without a new frame
// before the settle watchdog aborts it. 180 s is above the p99 of normal clip
// renders in the measured window, so a slow-but-advancing render is never
// cancelled (ELIM-06).
const DefaultRenderStallAfter = 180 * time.Second

// RenderQueueCanceller is the optional capability the watchdog needs to release
// the GPU lane. A client without it still reports the stall but cannot cancel.
type RenderQueueCanceller interface {
	Cancel(ctx context.Context, id string) error
}

// RenderStalledError is the typed stall report: child id, last known frame and
// the age of that observation. The message is the only diagnostic an operator
// gets when the lane is released, so it names all three.
type RenderStalledError struct {
	JobID          string
	FramesDone     int
	TotalFrames    int
	LastProgressAt time.Time
	StalledFor     time.Duration
	CancelErr      error
}

func (e *RenderStalledError) Error() string {
	if e == nil {
		return ErrRenderQueueStalled.Error()
	}
	frames := fmt.Sprintf("frames %d", e.FramesDone)
	if e.TotalFrames > 0 {
		frames = fmt.Sprintf("frames %d/%d", e.FramesDone, e.TotalFrames)
	}
	last := "never"
	if !e.LastProgressAt.IsZero() {
		last = e.LastProgressAt.UTC().Format(time.RFC3339)
	}
	message := fmt.Sprintf("%s: render job %s stalled in %s for %s (last progress %s)",
		ErrRenderQueueStalled, e.JobID, frames, e.StalledFor.Round(time.Second), last)
	if e.CancelErr != nil {
		message += fmt.Sprintf("; cancel failed: %v", e.CancelErr)
	}
	return message
}

// Unwrap makes errors.Is(err, ErrRenderQueueStalled) the classifier.
func (e *RenderStalledError) Unwrap() error { return ErrRenderQueueStalled }

// WaitRenderQueueTerminalWatched is the progress-aware bounded wait. When the
// client exposes RenderQueueProgressWaiter it parks on the long poll, which
// returns Progress with the current job; otherwise it samples with Get. It
// aborts a render whose last recorded PROGRESS is older than stallAfter.
//
// Two rules keep it safe:
//   - only progress counts, never absolute age: a render that keeps reporting
//     frames is never cancelled, however long it runs; and
//   - a job that has not started (no StartedAt, no Progress) is an admission
//     wait, not a stall, and is left to the budget.
//
// A stall is reported as a typed RenderStalledError and the remote job is
// cancelled when the client exposes RenderQueueCanceller, so the lane is freed
// instead of being held to the end of the settle window.
func WaitRenderQueueTerminalWatched(ctx context.Context, client RenderQueueClient, id string, interval, budget, stallAfter time.Duration) (RenderQueueJob, RenderCompletionMetrics, error) {
	if client == nil {
		return RenderQueueJob{}, RenderCompletionMetrics{}, fmt.Errorf("render queue wait: client is not configured")
	}
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	if stallAfter <= 0 {
		stallAfter = DefaultRenderStallAfter
	}
	waitStarted := time.Now()
	metrics := RenderCompletionMetrics{PollInterval: interval}
	deadline := time.Time{}
	if budget > 0 {
		deadline = waitStarted.Add(budget)
	}
	// Event-driven progress: when the queue exposes the long poll, park on it in
	// bounded slices. A progress-carrying wait returns the job (and Progress)
	// directly, so the watchdog needs ONE request per slice, not a park plus a
	// second Get. Clients without that capability keep the old sample-by-Get
	// fallback. A 30 s slice keeps stall detection well inside stallAfter while
	// cutting the request count by two orders of magnitude (CR-03).
	waiter, canPark := client.(RenderQueueWaiter)
	progressWaiter, canSampleProgress := client.(RenderQueueProgressWaiter)
	progressSlice := watchedProgressSlice
	var lastJob RenderQueueJob
	for {
		sampled := false
		if canPark {
			slice := progressSlice
			if !deadline.IsZero() {
				remaining := time.Until(deadline)
				if remaining <= 0 {
					metrics.CompletionWait = time.Since(waitStarted)
					return lastJob, metrics, fmt.Errorf("render queue job %s still %s after %s: %w", id, lastJob.State, budget, ErrRenderQueuePending)
				}
				if remaining < slice {
					slice = remaining
				}
			}
			parkCtx, cancel := context.WithTimeout(ctx, slice)
			var job RenderQueueJob
			var err error
			if canSampleProgress {
				job, err = progressWaiter.WaitProgress(parkCtx, id)
			} else {
				job, err = waiter.WaitTerminal(parkCtx, id)
			}
			cancel()
			metrics.PollCount++
			if err != nil {
				if ctx.Err() != nil {
					metrics.CompletionWait = time.Since(waitStarted)
					return RenderQueueJob{}, metrics, ctx.Err()
				}
				// A failure BEFORE the slice expired is a transport fault, not a
				// progress deadline: surface it like a failed Get. A slice expiry
				// falls through to the progress sample below. A progress waiter
				// may return its last observation with a deadline error, so keep
				// that job rather than spending another Get.
				if parkCtx.Err() == nil {
					return RenderQueueJob{}, metrics, err
				}
				if canSampleProgress && lastJob.ID != "" {
					sampled = true
				}
			} else {
				lastJob = job
				sampled = canSampleProgress
				if terminalRenderState(job.State) {
					metrics.CompletionWait = time.Since(waitStarted)
					return terminalRenderResult(job, id, metrics)
				}
			}
		}

		if !sampled {
			job, err := client.Get(ctx, id)
			metrics.PollCount++
			if err != nil {
				return RenderQueueJob{}, metrics, err
			}
			lastJob = job
		}
		job := lastJob
		if terminalRenderState(job.State) {
			metrics.CompletionWait = time.Since(waitStarted)
			return terminalRenderResult(job, id, metrics)
		}
		now := time.Now()
		lastProgress := job.StartedAt
		framesDone, totalFrames := 0, 0
		if job.Progress != nil {
			if job.Progress.LastFrameAt.After(lastProgress) {
				lastProgress = job.Progress.LastFrameAt
			}
			framesDone, totalFrames = job.Progress.FramesDone, job.Progress.TotalFrames
		}
		if !lastProgress.IsZero() && now.Sub(lastProgress) >= stallAfter {
			stalled := &RenderStalledError{
				JobID: id, FramesDone: framesDone, TotalFrames: totalFrames,
				LastProgressAt: lastProgress, StalledFor: now.Sub(lastProgress),
			}
			if canceller, ok := client.(RenderQueueCanceller); ok {
				if cancelErr := canceller.Cancel(ctx, id); cancelErr != nil {
					stalled.CancelErr = cancelErr
				}
			}
			metrics.CompletionWait = time.Since(waitStarted)
			return job, metrics, stalled
		}
		if !deadline.IsZero() && !now.Before(deadline) {
			metrics.CompletionWait = time.Since(waitStarted)
			return job, metrics, fmt.Errorf("render queue job %s still %s after %s: %w", id, job.State, budget, ErrRenderQueuePending)
		}
		if canPark {
			// The next WaitTerminal parks; no fixed sleep is spent.
			continue
		}
		sleep := interval
		if !deadline.IsZero() {
			if remaining := time.Until(deadline); remaining < sleep {
				sleep = remaining
			}
		}
		if sleep <= 0 {
			continue
		}
		timer := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			metrics.CompletionWait = time.Since(waitStarted)
			return RenderQueueJob{}, metrics, ctx.Err()
		case <-timer.C:
		}
	}
}

// WaitRenderQueueTerminalBounded is the deferrable form of
// WaitRenderQueueTerminal: it waits at most budget for the terminal state and
// reports ErrRenderQueuePending when the render outlives it.
//
// budget <= 0 is the historical blocking wait (one call parks until the render
// finishes), which is why every existing caller keeps its exact behaviour by
// passing 0. The bounded form exists so ONE settle path can be re-dispatched
// instead of pinned: the caller defers, and the retry asks again.
func WaitRenderQueueTerminalBounded(ctx context.Context, client RenderQueueClient, id string, interval, budget time.Duration) (RenderQueueJob, RenderCompletionMetrics, error) {
	if client == nil {
		return RenderQueueJob{}, RenderCompletionMetrics{}, fmt.Errorf("render queue wait: client is not configured")
	}
	if budget <= 0 {
		return WaitRenderQueueTerminal(ctx, client, id, interval)
	}
	waitCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	job, metrics, err := WaitRenderQueueTerminal(waitCtx, client, id, interval)
	if err == nil {
		return job, metrics, nil
	}
	// A budget expiry is the deferral signal. It is detected from the WAIT's own
	// context rather than from the error text: the waiter may surface it as a
	// context error, a transport error or a wrapped client error depending on
	// which path (event-driven or polling fallback) was taken, and the fact that
	// matters is "my window closed, the render did not". The CALLER's context is
	// checked too, so a caller-side cancellation is never mistaken for a wait.
	if ctx.Err() == nil && (waitCtx.Err() != nil || errors.Is(err, context.DeadlineExceeded)) {
		metrics.CompletionWait = budget
		// The polling fallback cannot report the job it was watching (a context
		// end has no job to return), so the address is normalized from the
		// caller's own id: the pending result must ALWAYS carry the remote
		// address, because that address is what the next attempt resumes from.
		if strings.TrimSpace(job.ID) == "" {
			job.ID = id
		}
		return job, metrics, fmt.Errorf("render queue job %s still %s after %s: %w", id, job.State, budget, ErrRenderQueuePending)
	}
	return job, metrics, err
}

// RearmFailedRenderJob re-arms the render job that a submission COLLIDED with
// (Submit answered ErrJobExists) when that job is already in FAILED state.
//
// It owns ONE decision, shared by the two callers that can collide with a
// pre-existing job — the overlay enqueuer (QueueRenderEnqueuer) and the
// clip.render executor (renderinggen.ClipRenderExecutor.Submit). An ErrJobExists
// replay of a FAILED job is NOT an idempotent success: the job can never produce
// an artifact, so a caller that moves on waits for something that cannot happen
// and finally reports an unexplained render failure. Before this helper existed,
// each caller carried its own copy of the rule — one swallowed the retry error,
// one used an anonymous interface — so the same fact had three answers.
//
// Semantics:
//   - the job is not in FAILED state, or cannot be read: no-op, nil. Waiting on
//     an existing job stays the right move here; the recovery is deliberately
//     best-effort and must not turn a transient read error into a submit failure.
//   - the job is FAILED and the client exposes RenderQueueRetrier: the retry
//     error is returned, never swallowed.
//   - the job is FAILED and the client has no retrier capability: fail closed
//     with an error naming the missing capability (the caller has no other way
//     to make progress, and pretending otherwise only hides the fault).
func RearmFailedRenderJob(ctx context.Context, client RenderQueueClient, id string) error {
	if client == nil {
		return fmt.Errorf("render queue re-arm: client is not configured")
	}
	existing, getErr := client.Get(ctx, id)
	if getErr != nil || existing.State != RenderQueueStateFailed {
		return nil
	}
	retrier, ok := client.(RenderQueueRetrier)
	if !ok {
		return fmt.Errorf("render queue job %s exists in failed state but the queue client cannot retry it (RenderQueueRetrier is not implemented)", id)
	}
	if retryErr := retrier.Retry(ctx, id); retryErr != nil {
		return fmt.Errorf("render queue retry failed for %s: %w", id, retryErr)
	}
	return nil
}
