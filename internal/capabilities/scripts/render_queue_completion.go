// Package scriptgeneration — render_queue_completion.go owns the render queue's
// COMPLETION surface: what a terminal state means to the caller, how a caller
// waits for it, and how the worker's own phase durations are projected onto the
// canonical run.
//
// Extracted from render_queue.go to keep that file under the
// max_lines_per_file_strict=600 cap. This is a split of an existing file inside
// the same package — never a new subpackage and never a compat shim — so the
// registered package hotspot stays at its baseline. The companion file keeps
// the adapter surface: the enqueuer, its setters and the enqueue path.
package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
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

// Drive publication is outside the Chronon/GPU critical path. This worker
// bound is independent of GPU admission, which remains owned by RenderingGen.
const defaultOverlayPublicationWorkers = 6
const maxOverlayPublicationWorkers = 8

// NewQueueRenderEnqueuer creates a queue-backed Chronon render enqueuer.
func NewQueueRenderEnqueuer(client RenderQueueClient) (*QueueRenderEnqueuer, error) {
	if client == nil {
		return nil, fmt.Errorf("queue render enqueuer requires a queue client")
	}
	return &QueueRenderEnqueuer{client: client, pollInterval: defaultQueuePollInterval}, nil
}

func (e *QueueRenderEnqueuer) SetPollInterval(interval time.Duration) {
	if e != nil && interval > 0 {
		e.pollInterval = interval
	}
}

func (e *QueueRenderEnqueuer) SetRecorder(r RenderAttemptRecorder) {
	if e != nil {
		e.recorder = r
	}
}

func (e *QueueRenderEnqueuer) SetArtifactPublisher(p OverlayArtifactPublisher) {
	if e != nil {
		e.publisher = p
	}
}

func (e *QueueRenderEnqueuer) SetAsyncPublication(on bool) {
	if e == nil {
		return
	}
	e.asyncPublication = on
	if on && e.publicationSem == nil {
		e.publicationSem = make(chan struct{}, e.publicationWorkerCount())
	}
}

func (e *QueueRenderEnqueuer) SetPublicationWorkers(workers int) {
	if e == nil {
		return
	}
	if workers <= 0 {
		workers = defaultOverlayPublicationWorkers
	}
	if workers > maxOverlayPublicationWorkers {
		workers = maxOverlayPublicationWorkers
	}
	e.publicationWorkers = workers
}

func (e *QueueRenderEnqueuer) publicationWorkerCount() int {
	if e == nil || e.publicationWorkers <= 0 {
		return defaultOverlayPublicationWorkers
	}
	return e.publicationWorkers
}

func (e *QueueRenderEnqueuer) SetFreshRender(on bool) {
	if e != nil {
		e.freshRender = on
	}
}

func (e *QueueRenderEnqueuer) SetSeparateItemRenders(on bool) {
	if e != nil {
		e.separateItemRenders = on
	}
}

func (e *QueueRenderEnqueuer) SetItemRenderPool(workers int) {
	if e != nil {
		e.itemRenderPool = workers
	}
}

func (e *QueueRenderEnqueuer) itemRenderWorkers() int {
	if e != nil && e.itemRenderPool > 0 {
		return e.itemRenderPool
	}
	return defaultSeparateItemRenderWorkers
}

// recordRenderingGenPhases projects the worker-reported RenderingGen phase
// durations (materialize/plan/render/encode/probe/hash/objectstore_upload/
// drive_publish) into canonical run operations bound to ctx. Phases the
// worker did not report (zero) are skipped — a missing measurement is never
// recorded as zero. The queue wait is already a canonical WaitCompletion
// observation (waitForCompletion) and the job wall time is the run's own
// WallTimeMs, so neither is duplicated here.
//
// The operations are bound to StageOverlayRender, the stage the render phase
// is measured under. Binding them to StageProcess instead left the render's
// work attached to a stage that no phase produced: the operations could never
// be joined to a stage wall time, so the breakdown reported the render's cost
// under the enclosing audio stage and gave that stage a dominant operation
// from another subsystem.
func recordRenderingGenPhases(ctx context.Context, artifact *RenderArtifact) {
	if artifact == nil {
		return
	}
	phases := []struct {
		operation  kernobs.OperationName
		durationMS int64
	}{
		{kernobs.OperationMaterialize, artifact.MaterializeMS},
		{kernobs.OperationPlan, artifact.PlanMS},
		{kernobs.OperationRender, artifact.RenderMS},
		{kernobs.OperationEncode, artifact.EncodeMS},
		{kernobs.OperationProbe, artifact.ProbeMS},
		{kernobs.OperationHash, artifact.HashMS},
		{kernobs.OperationObjectStoreUpload, artifact.UploadMS},
		{kernobs.OperationDrivePublish, artifact.DrivePublishMS},
	}
	for _, phase := range phases {
		if phase.durationMS <= 0 {
			continue
		}
		kernobs.RecordOperation(ctx, kernobs.OperationInfo{
			Stage:     StageOverlayRender,
			Component: kernobs.ComponentRenderingGen,
			Operation: phase.operation,
		}, phase.durationMS)
	}
}

// recordQueueBoundaryPhases projects the two halves of the blocking queue
// boundary PipelineGen itself owns — the submit round-trip and the
// terminal-state wait — onto the run bound to ctx as canonical operations
// under StageOverlayRender, next to the worker-reported phases. With them the
// stage wall decomposes as submit + wait + Σ worker phases, and the dominant
// term of a slow overlay stage (waiting for the remote GPU lane) is joinable
// to the stage instead of living only in the run-level wait list. Each
// duration is measured by its owner exactly once: the caller timed the
// boundary on its own clock, the worker timed the phases, and the kernel
// never re-times either. The run-level WaitCompletion observation is kept —
// this is the stage-bound projection of the same measured interval, not a
// second timer. A non-positive duration records nothing: a missing
// measurement is never a fake zero.
func recordQueueBoundaryPhases(ctx context.Context, submitMS, waitMS int64) {
	for _, phase := range []struct {
		operation  kernobs.OperationName
		durationMS int64
	}{
		{kernobs.OperationSubmit, submitMS},
		{kernobs.OperationWaitCompletion, waitMS},
	} {
		if phase.durationMS <= 0 {
			continue
		}
		kernobs.RecordOperation(ctx, kernobs.OperationInfo{
			Stage:     StageOverlayRender,
			Component: kernobs.ComponentRenderQueue,
			Operation: phase.operation,
		}, phase.durationMS)
	}
}

// waitForCompletion parks until the queue reports the job terminal, and records
// the whole blocked interval as a completion wait on the bound run
// (RunReport.Waits), never as a stage: it is time spent waiting on the render
// queue, not pipeline CPU work.
//
// The wait itself is owned by WaitRenderQueueTerminal, so this path and the
// clip.render settle continuation cannot drift apart.
func (e *QueueRenderEnqueuer) waitForCompletion(ctx context.Context, id string) (RenderQueueJob, RenderCompletionMetrics, error) {
	waitStarted := time.Now()
	defer func() {
		finishedAt := time.Now()
		kernobs.RecordWait(ctx, kernobs.WaitInfo{
			Kind:       kernobs.WaitCompletion,
			Component:  kernobs.ComponentRenderQueue,
			StartedAt:  waitStarted,
			FinishedAt: finishedAt,
		})
	}()
	job, metrics, err := WaitRenderQueueTerminal(ctx, e.client, id, e.pollInterval)
	metrics.WaitStartedAt = waitStarted
	metrics.WaitFinishedAt = time.Now()
	return job, metrics, err
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
// interval <= 0 selects defaultQueuePollInterval, and only the fallback loop
// uses it.
func WaitRenderQueueTerminal(ctx context.Context, client RenderQueueClient, id string, interval time.Duration) (RenderQueueJob, RenderCompletionMetrics, error) {
	if client == nil {
		return RenderQueueJob{}, RenderCompletionMetrics{}, fmt.Errorf("render queue wait: client is not configured")
	}
	if interval <= 0 {
		interval = defaultQueuePollInterval
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

var semanticAssetIDSanitizer = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func semanticAssetLogicalPath(ref capoverlay.OverlayAssetRef) string {
	if strings.HasPrefix(strings.TrimSpace(ref.URL), "assets/") {
		return filepath.ToSlash(strings.TrimSpace(ref.URL))
	}
	id := semanticAssetIDSanitizer.ReplaceAllString(strings.TrimSpace(ref.AssetID), "_")
	if id == "" {
		id = "asset"
	}
	ext := filepath.Ext(ref.URL)
	if parsed, err := url.Parse(ref.URL); err == nil && parsed.Path != "" {
		ext = filepath.Ext(parsed.Path)
	}
	if ext == "" {
		switch strings.ToLower(strings.TrimSpace(strings.SplitN(ref.MediaType, ";", 2)[0])) {
		case "image/png", "image":
			ext = ".png"
		case "image/jpeg", "image/jpg":
			ext = ".jpg"
		case "video/mp4", "video/quicktime", "video":
			ext = ".mp4"
		case "font/ttf", "font":
			ext = ".ttf"
		}
	}
	return "assets/semantic/" + id + ext
}

// ── Attempt recording ────────────────────────────────────────────────
// Merged from render_queue_attempt.go so the scripts package stays at its
// registered file-count baseline: the attempt record is part of the same
// completion lifecycle this file owns.

func observeCompletionWait(wait RenderCompletionMetrics, outcome string) {
	if !wait.WaitStartedAt.IsZero() && !wait.WaitFinishedAt.IsZero() && !wait.WaitFinishedAt.Before(wait.WaitStartedAt) {
		observability.OverlayCompletionWaitSeconds.WithLabelValues(outcome).Observe(wait.WaitFinishedAt.Sub(wait.WaitStartedAt).Seconds())
	}
}

func optionalTimePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

// recordQueueAttempt preserves producer and queue-owned timestamps even when
// the queue submit/wait path fails. Missing queue timestamps remain NULL; no
// zero timestamp is manufactured. Recorder failures are logged and returned so
// the caller can retain the original failure and the recording fault together.
func (e *QueueRenderEnqueuer) recordQueueAttempt(
	ctx context.Context,
	jobID string,
	plan capoverlay.OverlayPlan,
	metadata *overlayItemPublicationMetadata,
	wait RenderCompletionMetrics,
	submitStartedAt time.Time,
	submitAcceptedAt time.Time,
	job *RenderQueueJob,
	artifactAvailableAt time.Time,
	outcome string,
) error {
	if e == nil || e.recorder == nil {
		return nil
	}
	var artifact *RenderArtifact
	if job != nil {
		artifact = job.Artifact
	}
	attempt := BuildRenderAttemptAnalyticsWithWait(jobID, plan, artifact, wait)
	if metadata != nil && metadata.ItemID != "" {
		attempt.ItemID = metadata.ItemID
	}
	attempt.SubmitStartedAt = optionalTimePointer(submitStartedAt)
	attempt.SubmitAcceptedAt = optionalTimePointer(submitAcceptedAt)
	attempt.WaitStartedAt = optionalTimePointer(wait.WaitStartedAt)
	attempt.WaitFinishedAt = optionalTimePointer(wait.WaitFinishedAt)
	if job != nil {
		attempt.QueueQueuedAt = optionalTimePointer(job.QueuedAt)
		attempt.QueueStartedAt = optionalTimePointer(job.StartedAt)
		attempt.QueueCompletedAt = optionalTimePointer(job.CompletedAt)
	}
	attempt.ArtifactAvailableAt = optionalTimePointer(artifactAvailableAt)
	attempt.Outcome = outcome
	if err := e.recorder.RecordAttempt(ctx, attempt); err != nil {
		log.Printf("record render attempt analytics attempt_id=%s: %v", jobID, err)
		return fmt.Errorf("record render attempt analytics: %w", err)
	}
	return nil
}
