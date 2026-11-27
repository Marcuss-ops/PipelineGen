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
package renderqueue

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script/renderqueue"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

// RenderingGen queue job states are wire facts owned by the queue; the
// terminal-state classifier that interprets them lives in the kernel contract
// (renderqueue.terminalRenderResult) together with the types it consumes.
const (
	RenderQueueStateCompleted = renderqueue.RenderQueueStateCompleted
	RenderQueueStateFailed    = renderqueue.RenderQueueStateFailed
	RenderQueueStateCancelled = renderqueue.RenderQueueStateCancelled
)

// Drive publication is outside the Chronon/GPU critical path. This worker
// bound is independent of GPU admission, which remains owned by RenderingGen.
const defaultOverlayPublicationWorkers = 6
const maxOverlayPublicationWorkers = 8

// NewQueueRenderEnqueuer creates a queue-backed Chronon render enqueuer.
func NewQueueRenderEnqueuer(client RenderQueueClient) (*QueueRenderEnqueuer, error) {
	if client == nil {
		return nil, fmt.Errorf("queue render enqueuer requires a queue client")
	}
	return &QueueRenderEnqueuer{client: client, pollInterval: DefaultQueuePollInterval}, nil
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

func (e *QueueRenderEnqueuer) SetArtifactPublisher(p renderqueue.OverlayArtifactPublisher) {
	if e != nil {
		e.publisher = p
	}
}

// SetDynamicMapVideoCacheDir wires the durable cache root for dynamic-map MP4s.
// VELOX_MAP_VIDEO_CACHE_DIR remains the operator override.
func (e *QueueRenderEnqueuer) SetDynamicMapVideoCacheDir(dir string) {
	if e != nil {
		e.mapVideoCacheDir = strings.TrimSpace(dir)
	}
}

// SetDynamicMapGPUGate wires the same host-wide admission gate used by
// RenderingGen. A nil gate leaves only explicitly CPU-profile map rendering
// available; GPU maps fail closed instead of bypassing shared admission.
func (e *QueueRenderEnqueuer) SetDynamicMapGPUGate(gate DynamicMapGPUAdmission) {
	if e != nil {
		e.mapGPUGate = gate
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

// SetOverlayRenderCache wires the content-addressed reuse index for per-item
// overlay clips. Passing nil keeps renders uncached.
// An empty epoch resolves at call time from OverlayRenderCacheEpoch().
func (e *QueueRenderEnqueuer) SetOverlayRenderCache(cache OverlayRenderCache, epoch string) {
	if e != nil {
		e.renderCache = cache
		e.renderCacheEpoch = strings.TrimSpace(epoch)
	}
}

// SetOverlayArtifactProber wires the object-store presence check applied to a
// cache hit. Nil means the index is trusted without verification.
func (e *QueueRenderEnqueuer) SetOverlayArtifactProber(prober OverlayArtifactProber) {
	if e != nil {
		e.renderCacheProber = prober
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
	return config.DefaultSeparateItemRenderWorkers
}

// recordRenderingGenPhases projects the worker-reported RenderingGen phase
// durations (materialize/plan/render/encode/probe/hash/objectstore_upload/
// drive_publish) into canonical run operations bound to ctx. Phases the
// worker did not report (zero) are skipped — a missing measurement is never
// recorded as zero. The queue wait is already a canonical WaitCompletion
// observation (waitForCompletion) and the job wall time is the run's own
// WallTimeMs, so neither is duplicated here.
//
// The operations are bound to kernobs.StageOverlayRender, the stage the render phase
// is measured under. Binding them to StageProcess instead left the render's
// work attached to a stage that no phase produced: the operations could never
// be joined to a stage wall time, so the breakdown reported the render's cost
// under the enclosing audio stage and gave that stage a dominant operation
// from another subsystem.
func recordRenderingGenPhases(ctx context.Context, artifact *contract.RenderArtifact) {
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
			Stage:     kernobs.StageOverlayRender,
			Component: kernobs.ComponentRenderingGen,
			Operation: phase.operation,
		}, phase.durationMS)
	}
}

// recordQueueBoundaryPhases projects the two halves of the blocking queue
// boundary PipelineGen itself owns — the submit round-trip and the
// terminal-state wait — onto the run bound to ctx as canonical operations
// under kernobs.StageOverlayRender, next to the worker-reported phases. With them the
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
			Stage:     kernobs.StageOverlayRender,
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
// WST-05: the wait carries the SAME progress watchdog as the clip.render
// settle path. The measured window contains a 1.264 s single completion wait on
// this path — the unwatched long poll parked a worker lane on a render whose
// frames never moved, with no stall deadline. WaitRenderQueueTerminalWatched
// cancels a render that stops advancing and reports the typed stall instead.
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
	job, metrics, err := renderqueue.WaitRenderQueueTerminalWatched(
		ctx, e.client, id, e.pollInterval, 0, e.renderStallAfter())
	metrics.WaitStartedAt = waitStarted
	metrics.WaitFinishedAt = time.Now()
	return job, metrics, err
}

// renderStallAfter resolves the effective progress-watchdog window.
func (e *QueueRenderEnqueuer) renderStallAfter() time.Duration {
	if e != nil && e.stallAfter > 0 {
		return e.stallAfter
	}
	return renderqueue.DefaultRenderStallAfter
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
// ErrRenderQueuePending is the kernel-owned deferral sentinel.
var ErrRenderQueuePending = renderqueue.ErrRenderQueuePending

// WaitRenderQueueTerminalBounded is the deferrable form of
// WaitRenderQueueTerminal: it waits at most budget for the terminal state and
// reports ErrRenderQueuePending when the render outlives it.
//
// budget <= 0 is the historical blocking wait (one call parks until the render
// finishes), which is why every existing caller keeps its exact behaviour by
// passing 0. The bounded form exists so ONE settle path can be re-dispatched
// instead of pinned: the caller defers, and the retry asks again.
func WaitRenderQueueTerminalBounded(ctx context.Context, client RenderQueueClient, id string, interval, budget time.Duration) (RenderQueueJob, RenderCompletionMetrics, error) {
	return renderqueue.WaitRenderQueueTerminalBounded(ctx, client, id, interval, budget)
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
	return renderqueue.RearmFailedRenderJob(ctx, client, id)
}

var semanticAssetIDSanitizer = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func semanticAssetLogicalPath(ref capmodel.OverlayAssetRef) string {
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
		case "audio/mp4":
			ext = ".m4a"
		case "audio/mpeg":
			ext = ".mp3"
		case "audio/wav", "audio/x-wav", "audio":
			ext = ".wav"
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
	plan capmodel.OverlayPlan,
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
	var artifact *contract.RenderArtifact
	if job != nil {
		artifact = job.Artifact
	}
	attempt := BuildRenderAttemptAnalyticsWithWait(jobID, plan, artifact, wait)
	if metadata != nil && metadata.ItemID != "" {
		attempt.ItemID = metadata.ItemID
	}
	if metadata != nil && metadata.BatchID != "" {
		attempt.BatchID = metadata.BatchID
		attempt.BatchItemIDs = append([]string(nil), metadata.BatchItemIDs...)
		attempt.BatchStartUS = metadata.SourceStartUS
		attempt.BatchEndUS = metadata.BatchEndUS
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
