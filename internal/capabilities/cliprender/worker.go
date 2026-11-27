package cliprender

// worker.go is the canonical Master job handler for clip.render.
//
// Pipeline:
//
//	decode payload → Normalize + Validate → parallel preparation
//	(Preparer) → compile ASS artifact (when subtitles enabled) →
//	compile + seal ClipRenderPlanV1 → RenderingGen/Chronon render.
//
// The renderer and publisher remain mandatory at execution time: a plan
// that is only sealed or only rendered locally is never reported as a
// successful clip.
//
// Handle is an ORCHESTRATOR (ticket CR-19-05): each phase that used to live
// inline — loading the phase inputs, resolving the destination folder, the
// deterministic-cache hit, the submit half, the settle half — is its own method.
// The split is by data dependency, not by line count: the submit and settle
// halves share no mutable state beyond the sealed plan.

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

// ErrRenderPhaseNotImplemented is retained for the fail-closed case where a
// composition root exposes the job without attaching a render executor.
var ErrRenderPhaseNotImplemented = errors.New("clip.render: render executor is not wired")

// ErrInvalidJobPayload is the typed sentinel for an undecodable job payload.
// Terminal: retrying the same payload can never succeed.
var ErrInvalidJobPayload = errors.New("clip.render: invalid job payload")

// ErrRenderPublisherNotWired is the typed sentinel for a render that produced
// certified bytes but has no publication boundary attached.
//
// A clip.render job's deliverable IS the published, committed derived asset:
// the worker header and WithRenderPublisher both declare publication mandatory,
// and the registered policy classifies clip.render as
// ArtifactOwnershipApplication. A composition root that exposes the job without
// the publisher therefore has a configuration fault, not a render that is
// "done enough" — reporting it as a completed clip would hand the operator a
// SUCCEEDED job whose artifact was never uploaded or committed. Terminal by
// construction: no retry can attach the missing port.
var ErrRenderPublisherNotWired = errors.New("clip.render: render publisher is not wired")

// Worker is the canonical clip.render job handler. It is constructed with
// the Preparer and bound to the Master via
// job.Service.RegisterHandler(TypeClipRender, job.HandlerFunc(worker.Handle)).
type Worker struct {
	preparer             *Preparer
	inputRevisions       InputDigestResolver // CRA-03 guard; defaults to preparer
	workspaceDir         string
	subtitles            SubtitleCompiler          // optional until the ASS-compiler step wires it
	renderer             RenderExecutor            // optional until the render-phase step consumes it
	continuationStore    ContinuationStore         // required for the async submit/settle path
	continuationEnqueuer ContinuationEnqueuer      // required for the async submit/settle path
	publisher            RenderPublisher           // optional in unit tests; required by production wiring
	folderResolver       DestinationFolderResolver // optional: required only when a request carries destination.subfolder_name
	overlayResolver      OverlaySegmentResolver    // required when a request declares an overlay
	renderCache          RenderCache               // optional: deterministic fingerprint → certified locator
	chrononMetrics       *ChrononMetricsAdapter    // optional: Chronon phase projection (performance_operations)
	chrononTimingFetcher ChrononTimingFetcher      // optional: fetches the raw timing sidecar by content address
	log                  *zap.Logger
	// settleWait is the per-attempt bounded wait on the remote render
	// (DefaultSettleWait), and settleWindow bounds how long the settle
	// continuation may keep deferring before it waits the render out
	// (DefaultSettleWindow). Both are owned by this worker because they are
	// POLICY (how long a lane may be held, how often to ask); the executor owns
	// only the transport. See settleWaitBudget.
	settleWait   time.Duration
	settleWindow time.Duration
	// settleRetry is the re-dispatch delay AFTER a bounded attempt expires. It
	// is deliberately shorter than settleWait: the attempt budget bounds how
	// long a lane is held, not how long a finished render may wait to be
	// collected (CRA-08).
	settleRetry time.Duration
	// settleDeferrals/settleDeferredMS are the explicit CRA-08 counters: how
	// many times a settle handed its attempt back and how much extra collection
	// latency those deferrals announced. They are process-local observability,
	// not the durable job state (the scheduler owns DeferredUntil).
	settleDeferrals  atomic.Int64
	settleDeferredMS atomic.Int64
	// sequence is the assembly phase (sequence.go). Nil means the phase is not
	// wired and a sequence job fails closed instead of rendering silently.
	sequence *sequenceAssembly
}

// NewWorker constructs the canonical worker. Fail-closed: preparer and log
// are mandatory; workspaceDir is the scratch root for run artifacts
// (rendered-clip.mp4 + subtitles.ass land under workspaceDir/runs/<run-id>/).
func NewWorker(preparer *Preparer, workspaceDir string, log *zap.Logger) (*Worker, error) {
	if preparer == nil {
		return nil, fmt.Errorf("cliprender.NewWorker: Preparer is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Worker{
		preparer:       preparer,
		inputRevisions: preparer,
		workspaceDir:   workspaceDir,
		log:            log,
		settleWait:     DefaultSettleWait,
		settleWindow:   DefaultSettleWindow,
		settleRetry:    DefaultSettleRetryDelay,
	}, nil
}

// DefaultSettleWait is how long ONE settle attempt holds its worker lane on a
// running remote render before handing the attempt back. It is set above the
// observed short-clip render so the common case still completes inside a single
// attempt (no added latency, no extra dispatch), while a render that outlives it
// no longer pins a lane for minutes or hours.
const DefaultSettleWait = 60 * time.Second

// DefaultSettleWindow is how long the settle continuation may keep ASKING (one
// bounded attempt per dispatch) before it stops deferring and waits the render
// out to completion in a single attempt. Without this bound a render that never
// reports terminal would be re-dispatched forever; with it, the settle degrades
// to the historical blocking behaviour instead of looping.
const DefaultSettleWindow = 20 * time.Minute

// DefaultSettleRetryDelay is the re-dispatch delay after one bounded settle
// attempt expires. It is intentionally much shorter than DefaultSettleWait: a
// render that finishes just after the budget must not wait another full budget
// before it is collected. The attempt budget still bounds lane occupancy; this
// only sets how soon the next (cheap) attempt asks again.
const DefaultSettleRetryDelay = 2 * time.Second

// WithSettleWait sets the per-attempt bounded wait. 0 (or negative) disables
// the deferral entirely: every settle waits the render out, exactly the
// pre-deferral behaviour.
func (w *Worker) WithSettleWait(wait time.Duration) *Worker {
	if w != nil {
		w.settleWait = wait
	}
	return w
}

// SettleDeferralStats reports the explicit CRA-08 counters: the number of
// deferrals this worker produced and the total re-dispatch delay it asked for.
// A nil worker reports zeroes.
func (w *Worker) SettleDeferralStats() (count, addedLatencyMS int64) {
	if w == nil {
		return 0, 0
	}
	return w.settleDeferrals.Load(), w.settleDeferredMS.Load()
}

// WithSettleRetry sets the re-dispatch delay after a bounded attempt expires.
// 0 (or negative) keeps the historical behaviour and reuses the attempt budget
// as the delay.
func (w *Worker) WithSettleRetry(delay time.Duration) *Worker {
	if w != nil {
		w.settleRetry = delay
	}
	return w
}

// WithSettleWindow sets how long the settle continuation may keep deferring.
func (w *Worker) WithSettleWindow(window time.Duration) *Worker {
	if w != nil {
		w.settleWindow = window
	}
	return w
}

// settleWaitBudget decides how long THIS attempt may wait on the remote render:
// the bounded wait inside the deferral window, or 0 (wait to completion) once
// the window has closed. The window is measured from the settle child's own
// creation, so it is durable — a restart resumes the same budget instead of
// restarting the clock.
func (w *Worker) settleWaitBudget(j *job.Job) time.Duration {
	if w == nil || w.settleWait <= 0 || w.settleWindow <= 0 {
		return 0
	}
	if j == nil || j.CreatedAt.IsZero() {
		// No durable age to reason about: hold the bounded wait (the render is
		// tracked by its own handle, and the caller can always ask again).
		return w.settleWait
	}
	if time.Since(j.CreatedAt) >= w.settleWindow {
		return 0
	}
	return w.settleWait
}

// Handle dispatches one clip.render job: the assembly phase, or the
// submit/settle halves of a render. It owns only orchestration — every phase
// below owns its own validation, error contract and observability.
func (w *Worker) Handle(ctx context.Context, j *job.Job, tools *job.JobExecutionTools) (job.Result, error) {
	// The sequence phase renders nothing, so it is dispatched BEFORE the render
	// phases are reported: a sequence job must never appear as a clip render.
	if isSequencePayload(j.Payload) {
		return w.handleSequence(ctx, j, tools)
	}
	progress := safeProgress(tools)
	emit := safeEvent(tools)

	progress(0, "clip.render started")
	jobStart := time.Now()
	// submitted is the real queue wait: from the durable creation time to the
	// instant this handler started. A zero-width marker measured nothing and
	// made queue wait indistinguishable from a real zero (CR-08). claimed has no
	// durable timestamp in this job shape, so it is omitted rather than faked as
	// a zero-width phase.
	if !j.CreatedAt.IsZero() && !jobStart.Before(j.CreatedAt) {
		kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseSubmitted, j.CreatedAt, jobStart, kernobs.StageStatusCompleted, nil)
	}
	// Per-phase progress is a Debug trace: the canonical operator events for a
	// clip are accepted (clip.render.job.start), completed
	// (clip.render.job.completed) and failed. Emitting one Info line per phase
	// here cost ~6 lines per clip for facts the RunReport already owns.
	w.log.Debug("clip.render.job.phase",
		zap.String("subsystem", "clip_render_worker"),
		zap.String("phase", "start"),
		zap.String("job_id", j.ID),
	)

	phase, continuation, err := decodeContinuationPayload(j.Payload)
	if err != nil {
		return nil, err
	}

	inputs, err := w.loadPhaseInputs(ctx, j, phase, continuation)
	if err != nil {
		return nil, err
	}
	req := inputs.request

	w.log.Info("clip.render.job.start",
		zap.String("subsystem", "clip_render_worker"),
		zap.String("job_id", j.ID),
		zap.String("source_asset_id", req.SourceAssetID),
		zap.String("destination_folder_id", req.Destination.DriveFolderID),
		zap.Bool("subtitles_enabled", req.Subtitles.Enabled),
		zap.Bool("watermark_requested", req.Watermark.Enabled),
		zap.Bool("background_mode", req.Background.Mode != ""),
		zap.Int("overlay_segments_requested", len(req.Overlays)),
		zap.Bool("require_gpu", req.Execution.RequireGPU),
		zap.Bool("require_zero_copy", req.Execution.RequireZeroCopy),
	)

	// Deterministic cache FIRST: a hit returns the previously certified and
	// published locator, so it needs neither preparation nor the Drive leaf
	// resolution. The fingerprint already carries the destination identity
	// (drive_folder_id + subfolder_name), so a hit cannot cross destinations;
	// the resolved leaf is only consumed by a job that actually renders,
	// publishes and delivers. Resolving before this probe made every duplicate
	// of an already-certified clip pay one Drive round-trip (measured: 882 ms
	// average, 1.13 s in the 1.8 s cache-hit path) for a folder it never used.
	if phase != RenderPhaseSettle {
		if result, hit := w.tryCachedCompletion(ctx, j, &req, jobStart, progress); hit {
			return result, nil
		}
	}

	publishFolderID, err := w.resolvePublishFolder(ctx, j, &req, phase, inputs.resumeFolder)
	if err != nil {
		return nil, err
	}

	// Destination-independent reuse: another folder already certified this
	// render, so this job only delivers it. Placed after the folder resolution
	// and before preparation, which is the expensive part it removes.
	if phase != RenderPhaseSettle {
		result, reused, reuseErr := w.tryRenderReuse(ctx, j, &req, publishFolderID, jobStart, progress)
		if reuseErr != nil {
			return nil, reuseErr
		}
		if reused {
			return result, nil
		}
	}

	if phase == RenderPhaseSettle {
		progress(90, "remote render submitted; settling certified artifact")
	} else {
		progress(10, "request validated; running parallel preparation")
		// A broker RETRY re-enters here. The durable job → resume index lets a
		// retry that happens after the seal load the prepared plan instead of
		// repeating asset resolution, materialization, subtitle compilation and
		// sealing (CRA-07). The index is an accelerator: a missing/corrupt entry,
		// a different request or a foreign plan falls back to full preparation,
		// and every durable step remains idempotent either way.
		if reused, ok := w.loadSubmitCheckpoint(ctx, j, &req); ok {
			inputs.prepared = reused.prepared
			inputs.plan = reused.plan
			inputs.subtitles = reused.subtitles
			inputs.subtitleCompileMS = reused.subtitleCompileMS
			inputs.fromCheckpoint = true
			progress(15, "prepared plan resumed from checkpoint")
		} else {
			inputs.prepared, inputs.plan, inputs.subtitles, inputs.subtitleCompileMS, err = w.preparePlan(ctx, &req, j.ID, emit)
			if err != nil {
				return nil, err
			}
		}
	}

	emit("clip.render.plan.sealed", "ClipRenderPlanV1 sealed — fully resolved before Chronon", map[string]any{
		"plan_version": inputs.plan.Version,
		"plan_sha256":  inputs.plan.PlanSHA256,
		"output_path":  inputs.plan.OutputPath,
		"source":       inputs.plan.Source.Path,
		"subtitles":    inputs.plan.Subtitles != nil,
		"watermark":    inputs.plan.Watermark != nil,
		"background":   inputs.plan.Background.Mode,
	})
	if w.renderer == nil {
		result := renderedResult(j, &req, inputs.prepared, inputs.plan, inputs.subtitles, nil, nil)
		result["phase"] = "plan_sealed"
		addQueueWaitMS(result, j, jobStart)
		return result, fmt.Errorf(
			"%w: job_id=%s source_asset_id=%s plan_sha256=%s",
			ErrRenderPhaseNotImplemented, j.ID, req.SourceAssetID, inputs.plan.PlanSHA256)
	}

	if phase == RenderPhaseSubmit {
		return w.runSubmitPhase(ctx, j, &req, inputs, publishFolderID, progress)
	}
	return w.settleAndComplete(ctx, j, tools, jobStart, &req, inputs, publishFolderID, continuation, progress)
}
