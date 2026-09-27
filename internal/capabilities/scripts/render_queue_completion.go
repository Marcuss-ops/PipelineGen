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
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

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
		kernobs.RecordWait(ctx, kernobs.WaitInfo{
			Kind:       kernobs.WaitCompletion,
			Component:  kernobs.ComponentRenderQueue,
			StartedAt:  waitStarted,
			FinishedAt: time.Now(),
		})
	}()
	return WaitRenderQueueTerminal(ctx, e.client, id, e.pollInterval)
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
