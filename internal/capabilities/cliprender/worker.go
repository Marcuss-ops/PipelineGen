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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
		preparer:     preparer,
		workspaceDir: workspaceDir,
		log:          log,
		settleWait:   DefaultSettleWait,
		settleWindow: DefaultSettleWindow,
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

// WithSettleWait sets the per-attempt bounded wait. 0 (or negative) disables
// the deferral entirely: every settle waits the render out, exactly the
// pre-deferral behaviour.
func (w *Worker) WithSettleWait(wait time.Duration) *Worker {
	if w != nil {
		w.settleWait = wait
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

	if phase == RenderPhaseSettle {
		progress(90, "remote render submitted; settling certified artifact")
	} else {
		progress(10, "request validated; running parallel preparation")
		// A broker RETRY of this job re-enters here and re-runs preparation,
		// because a retry re-executes the whole payload from the top.
		//
		// That is a deliberate, bounded cost, not an oversight. The remote job id
		// IS derivable before preparation (plan.RunID == j.ID), but the resume
		// document's ADDRESS is its own content digest, so "has this job already
		// submitted?" cannot be asked without a durable index from job id to
		// resume digest — a second owner for a fact the continuation payload
		// already carries. Until such an index is justified, a retry re-does
		// LOCAL work only: every durable step is idempotent (Prepare re-resolves
		// the same assets, the content-addressed resume document re-puts under
		// the same digest, Submit re-addresses the same remote job and answers
		// ErrJobExists, the settle enqueue dedupes on ActiveKey), so a retry can
		// never duplicate a render, an artifact or a delivery.
		inputs.prepared, inputs.plan, inputs.subtitles, inputs.subtitleCompileMS, err = w.preparePlan(ctx, &req, j.ID, emit)
		if err != nil {
			return nil, err
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

// phaseInputs is the immutable snapshot one phase starts from. The submit half
// fills the preparation results; a settle continuation loads them from its
// resume document (it never re-prepares).
type phaseInputs struct {
	request           RenderRequest
	prepared          *Prepared
	plan              ClipRenderPlanV1
	subtitles         *SubtitleArtifact
	resumeFolder      string
	subtitleCompileMS int64
}

// loadPhaseInputs decodes the payload for the phase: a submit job decodes the
// request; a settle continuation loads the content-addressed resume document
// and verifies that it describes the plan that was actually submitted.
func (w *Worker) loadPhaseInputs(ctx context.Context, j *job.Job, phase RenderPhase, continuation *Continuation) (*phaseInputs, error) {
	inputs := &phaseInputs{subtitleCompileMS: -1}
	if phase == RenderPhaseSettle {
		if w.continuationStore == nil {
			return nil, fmt.Errorf("clip.render: settle phase requires a ContinuationStore")
		}
		doc, loadErr := w.continuationStore.GetResumeDocument(ctx, continuation.Resume)
		if loadErr != nil {
			return nil, fmt.Errorf("clip.render: load continuation: %w", loadErr)
		}
		if attrErr := doc.Attributes(continuation.Submission.RenderJobID, continuation.Submission.PlanSHA256); attrErr != nil {
			return nil, fmt.Errorf("clip.render: continuation binding: %w", attrErr)
		}
		inputs.request = doc.Request
		inputs.request.Normalize()
		if err := inputs.request.Validate(); err != nil {
			return nil, fmt.Errorf("%w: resume request: %v", ErrInvalidJobPayload, err)
		}
		inputs.plan = doc.Plan
		inputs.subtitles = doc.Subtitles
		inputs.resumeFolder = doc.PublishFolderID
		// Reuse the preparation measurements the submit half took. The settle
		// phase does not prepare anything, so these are the phase walls that
		// explain the submit side of the job wall; leaving them unset would
		// report measured work as NOT_INSTRUMENTED.
		if doc.SubtitleCompileMS != nil {
			inputs.subtitleCompileMS = *doc.SubtitleCompileMS
		}
		inputs.prepared = preparedFromResume(doc)
		return inputs, nil
	}

	if err := json.Unmarshal(j.Payload, &inputs.request); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJobPayload, err)
	}
	inputs.request.Normalize()
	if err := inputs.request.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJobPayload, err)
	}
	return inputs, nil
}

// resolvePublishFolder resolves the leaf Drive folder ONCE per job, never in
// the publisher.
//
// When the request carries destination.subfolder_name (a script/batch identity
// all its clips share), the worker resolves the leaf folder create-or-reuse
// through the canonical DestinationFolderResolver and hands the publisher the
// fully-resolved leaf ID. The publisher is dumb by contract: it never creates
// folders. Clips of the same batch carry the same subfolder_name, so each job
// resolves the same folder and they converge on one shared Drive directory.
// Without a subfolder_name the request's destination.drive_folder_id is already
// the resolved leaf and is passed through verbatim.
func (w *Worker) resolvePublishFolder(ctx context.Context, j *job.Job, req *RenderRequest, phase RenderPhase, resumeFolder string) (string, error) {
	if phase == RenderPhaseSettle {
		// Destination resolution and all preparation already happened before
		// Submit. Repeating it here would re-download assets and can select a
		// different Drive folder after a restart.
		return continuationResumeFolder(resumeFolder, req.Destination.DriveFolderID), nil
	}
	publishFolderID := req.Destination.DriveFolderID
	if strings.TrimSpace(req.Destination.SubfolderName) == "" {
		return publishFolderID, nil
	}
	if w.folderResolver == nil {
		return "", fmt.Errorf("clip.render: destination.subfolder_name=%q declared but no DestinationFolderResolver is wired (the publisher must never create folders)", req.Destination.SubfolderName)
	}
	resolveStart := time.Now()
	resolvedID, resolveErr := w.folderResolver.ResolveDestinationFolder(ctx, DestinationFolderResolveInput{
		RootFolderID:  req.Destination.DriveFolderID,
		SubfolderName: req.Destination.SubfolderName,
	})
	kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: StageClipDestinationResolve}, resolveStart, time.Now(), resolveErr)
	if resolveErr != nil {
		return "", fmt.Errorf("clip.render: resolve destination subfolder %q: %w", req.Destination.SubfolderName, resolveErr)
	}
	if strings.TrimSpace(resolvedID) == "" {
		return "", fmt.Errorf("clip.render: destination subfolder %q resolved to an empty folder ID", req.Destination.SubfolderName)
	}
	w.log.Debug("clip.render.job.destination_resolved",
		zap.String("subsystem", "clip_render_worker"),
		zap.String("job_id", j.ID),
		zap.String("root_folder_id", req.Destination.DriveFolderID),
		zap.String("subfolder_name", req.Destination.SubfolderName),
		zap.String("resolved_folder_id", resolvedID),
	)
	return resolvedID, nil
}

// tryCachedCompletion is the deterministic cache fast path at the worker
// level: identical semantics → same bytes without touching the GPU. The handler
// already probes the same cache, but the worker owns the authoritative hit: a
// completed render that committed its artifact may still race the handler's
// view.
//
// The returned result is deliberately HONEST about provenance (ticket
// CR-19-07): this job rendered nothing, so it reports neither preparation
// timings nor a plan digest it never sealed.
func (w *Worker) tryCachedCompletion(ctx context.Context, j *job.Job, req *RenderRequest, jobStart time.Time, progress func(int, string)) (job.Result, bool) {
	if w.renderCache == nil || req == nil {
		return nil, false
	}
	fp, fpErr := req.Fingerprint()
	if fpErr != nil {
		return nil, false
	}
	rec, err := w.renderCache.Get(ctx, fp)
	logCacheLookupFailure(w.log, "worker", fp, err)
	if err != nil || rec == nil {
		return nil, false
	}
	w.log.Info("clip.render cache hit (worker)",
		zap.String("job_id", j.ID),
		zap.String("fingerprint", digestPrefix(fp)),
		zap.String("asset_id", rec.AssetID),
	)
	// Synthesize a cached completion without touching Chronon.
	cached := &RenderOutcome{
		StorageKey:  rec.StorageKey,
		ArtifactURL: rec.ArtifactURL,
		ContentType: rec.ContentType,
		SHA256:      rec.SHA256,
		SizeBytes:   rec.SizeBytes,
		DurationSec: rec.DurationSec,
		Width:       rec.Width,
		Height:      rec.Height,
		FPSNum:      rec.FPSNum,
		FPSDen:      rec.FPSDen,
		Backend:     rec.Backend,
		Metrics:     NewRenderMetricsV2(),
	}
	if cached.Metrics != nil {
		cached.Metrics.BackendSelected = rec.Backend
	}
	// Fabricate a minimal Prepared for the SHARED result projection. Its
	// timings stay empty: no preparation ran in this job, and an empty timing
	// set is projected as absent rather than as a measured zero.
	// No Contract block: this job resolved none, and a synthesized one would
	// put a backend name in contract_id (ticket CR-19-19).
	synthPrepared := &Prepared{
		RunID:      j.ID,
		Source:     &MaterializedAsset{AssetID: req.SourceAssetID, Title: "cached"},
		Transcript: &TranscriptResult{},
	}
	// Reused, NOT republished: the record is written only after a publication
	// validated its AssetID, so a record existing IS the proof that the render
	// completed and was published. This job performs no upload and owes no
	// delivery, so the projection must claim neither PUBLISHED (a fact this job
	// never observed) nor PENDING (the historical fabrication, which announced a
	// Drive delivery that nobody would ever perform).
	publication := &RenderPublishResult{
		AssetID:        rec.AssetID,
		Reused:         true,
		SizeBytes:      rec.SizeBytes,
		DriveFileID:    rec.DriveFileID,
		DrivePending:   rec.DeliveryStatus == "pending",
		DeliveryStatus: rec.DeliveryStatus,
	}
	if rec.DeliveryStatus == "not_required" {
		publication.ContentURL = AssetContentURL(rec.AssetID)
	}
	res := renderedResult(j, req, synthPrepared, ClipRenderPlanV1{}, nil, cached, publication)
	// The job wall IS measured even on a cache hit, so it is reported; the
	// render phases stay NotInstrumented because no render ran, and Compute()
	// never derives a value from an uninstrumented input.
	finalizeMetrics(cached.Metrics, time.Since(jobStart).Milliseconds(), cached.DurationSec)
	res["cache_hit"] = true
	res["fingerprint"] = fp
	res["phase"] = "cached"
	res["delivery_status"] = rec.DeliveryStatus
	res["drive_file_id"] = rec.DriveFileID
	addQueueWaitMS(res, j, jobStart)
	progress(100, "clip.render cache hit")
	return res, true
}

// runSubmitPhase performs the first half of the async boundary: persist the
// resume document, hand the sealed plan to RenderingGen, and enqueue the settle
// continuation. It never waits for the render, so the Master slot is released
// here.
func (w *Worker) runSubmitPhase(ctx context.Context, j *job.Job, req *RenderRequest, inputs *phaseInputs, publishFolderID string, progress func(int, string)) (job.Result, error) {
	if w.continuationStore == nil || w.continuationEnqueuer == nil {
		return nil, fmt.Errorf("clip.render: executor is wired but continuation store/enqueuer is missing")
	}
	plan := inputs.plan
	remoteRenderID := plan.RunID
	if resolver, ok := w.renderer.(RenderJobIDResolver); ok {
		remoteRenderID = resolver.RenderJobID(plan)
	}
	if strings.TrimSpace(remoteRenderID) == "" {
		remoteRenderID = plan.RunID
	}
	resumeRemoteID := ""
	if remoteRenderID != plan.RunID {
		resumeRemoteID = remoteRenderID
	}
	doc := ResumeDocument{
		Plan:               plan,
		RemoteRenderID:     resumeRemoteID,
		Request:            *req,
		PublishFolderID:    publishFolderID,
		SourceTitle:        inputs.prepared.Source.Title,
		SourceSizeBytes:    inputs.prepared.Source.SizeBytes,
		Contract:           inputs.prepared.Contract,
		Transcript:         inputs.prepared.Transcript,
		Subtitles:          inputs.subtitles,
		PreparationTimings: inputs.prepared.Timings,
	}
	if inputs.subtitleCompileMS >= 0 {
		compileMS := inputs.subtitleCompileMS
		doc.SubtitleCompileMS = &compileMS
	}
	resumeRef, storeErr := w.continuationStore.PutResumeDocument(ctx, doc)
	if storeErr != nil {
		return nil, fmt.Errorf("clip.render: persist continuation: %w", storeErr)
	}
	submission := Submission{
		RenderJobID:   remoteRenderID,
		PlanSHA256:    plan.PlanSHA256,
		CorrelationID: j.CorrelationID,
		State:         RemoteRenderSubmitted,
		Attempt:       1,
	}
	if submitErr := w.renderer.Submit(ctx, plan); submitErr != nil {
		return nil, fmt.Errorf("clip.render: submit remote render: %w", submitErr)
	}
	continuation := Continuation{Submission: submission, Resume: resumeRef}
	childID, enqueueErr := w.continuationEnqueuer.EnqueueContinuation(ctx, ContinuationRequest{
		ParentJobID:  j.ID,
		ParentRunID:  j.ID,
		ActiveKey:    ActiveKeyFor(submission.RenderJobID, submission.Attempt),
		Continuation: continuation,
	})
	if enqueueErr != nil {
		return nil, fmt.Errorf("clip.render: enqueue settle continuation: %w", enqueueErr)
	}
	result := renderedResult(j, req, inputs.prepared, plan, inputs.subtitles, nil, nil)
	result["phase"] = "submitted"
	result["parent_state"] = ParentStateWaitingChildren
	result["child_job_id"] = childID
	result["render_submission"] = submission
	result["continuation"] = continuation
	progress(100, "remote render submitted; Master slot released")
	return result, nil
}

// settleAndComplete is the second half of the async boundary: wait (bounded) on
// the remote render, classify a budget expiry as a deferral rather than a
// failure, then run the ONE completion path shared with the blocking form.
func (w *Worker) settleAndComplete(
	ctx context.Context,
	j *job.Job,
	tools *job.JobExecutionTools,
	jobStart time.Time,
	req *RenderRequest,
	inputs *phaseInputs,
	publishFolderID string,
	continuation *Continuation,
	progress func(int, string),
) (job.Result, error) {
	plan := inputs.plan
	progress(90, "plan sealed; rendering with Chronon")
	// render_slot is the interval this Master job HOLDS its render slot. It is
	// measured from the renderer invocation to the materialized artifact, i.e.
	// the real occupancy the slot pool/gate bounds — never a zero-width marker
	// (the previous start==end form measured nothing and reported 0 ms of slot
	// wait for every render).
	// One occupancy interval, one clock read: the slot is held for exactly the
	// render boundary below.
	renderStart := time.Now()
	renderSlotStart := renderStart
	w.log.Debug("clip.render.job.phase",
		zap.String("subsystem", "clip_render_worker"),
		zap.String("phase", "render_start"),
		zap.String("job_id", j.ID),
		zap.String("plan_sha256", plan.PlanSHA256),
		zap.String("output_path", plan.OutputPath),
	)
	// How long THIS attempt may hold its lane on the render. The settle
	// continuation is the only phase that reaches here, so the budget is the
	// settle policy (see settleWaitBudget): zero means "wait the render out",
	// which is the historical blocking behaviour.
	waitBudget := w.settleWaitBudget(j)
	// The render boundary is both a stage (wall, owner-measured anchors) and
	// an operation (chronon.render_clip accumulated work) on the RunReport, so
	// the benchmark can compare render WALL against render WORK exactly like
	// the script.generate phases.
	outcome, err := func() (o *RenderOutcome, e error) {
		e = kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
			Stage:     StageClipRender,
			Component: kernobs.ComponentName("chronon"),
			Operation: kernobs.OperationName("render_clip"),
		}, func(opCtx context.Context) error {
			// Submit returned above with its result, so the only phase that
			// reaches the render boundary here is the settle continuation.
			var rErr error
			switch renderer := w.renderer.(type) {
			case RenderExecutorDeferring:
				o, rErr = renderer.SettleWithin(opCtx, plan, continuation.Submission.RenderJobID, waitBudget)
			case RenderExecutorWithJobID:
				o, rErr = renderer.SettleWithJobID(opCtx, plan, continuation.Submission.RenderJobID)
			default:
				o, rErr = w.renderer.Settle(opCtx, plan)
			}
			return rErr
		})
		return o, e
	}()
	renderEnd := time.Now()
	renderMS := renderEnd.Sub(renderStart).Milliseconds()
	// A bounded settle that ran out of budget is a WAIT, not a render failure.
	// It is classified BEFORE any failure recording so the render slot, the
	// clip phases and the run report do not report a working render as broken —
	// and, most importantly, so the attempt is handed back as a deferral (which
	// spends no retry budget) instead of ending as clip.render: FAILED.
	if errors.Is(err, ErrRenderPending) {
		kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseRenderSlot, renderSlotStart, renderEnd, kernobs.StageStatusCompleted, nil)
		w.log.Info("clip.render.job.settle_deferred",
			zap.String("subsystem", "clip_render_worker"),
			zap.String("job_id", j.ID),
			zap.String("render_job_id", continuation.Submission.RenderJobID),
			zap.Duration("wait_budget", waitBudget),
			zap.Int64("elapsed_ms", renderMS),
		)
		// The deferral delay IS the budget this attempt was willing to wait:
		// one poll cadence, one owner. A zero budget cannot reach here (a
		// budget of zero waits to completion), so the delay is always positive.
		return nil, job.DeferredAfter(waitBudget, fmt.Sprintf(
			"remote render %s is still running after %s", continuation.Submission.RenderJobID, waitBudget))
	}
	if errors.Is(err, ErrRenderStalled) {
		// The watchdog already cancelled the remote job: the lane is free. This
		// is a FAILURE, not a wait — retrying the same render identity would
		// park the lane again on a render that never advanced (ELIM-06).
		kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseRenderSlot, renderSlotStart, renderEnd, kernobs.StageStatusFailed, err)
		kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: StageClipRender}, renderStart, renderEnd, err)
		w.log.Error("clip.render.job.settle_stalled",
			zap.String("subsystem", "clip_render_worker"),
			zap.String("job_id", j.ID),
			zap.String("render_job_id", continuation.Submission.RenderJobID),
			zap.Int64("elapsed_ms", renderMS),
			zap.Error(err),
		)
		return nil, err
	}
	renderStatus := kernobs.StageStatusCompleted
	if err != nil {
		renderStatus = kernobs.StageStatusFailed
	}
	kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseRenderSlot, renderSlotStart, renderEnd, renderStatus, err)
	kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: StageClipRender}, renderStart, renderEnd, err)
	// Chronon owns compositing and encoding as one render boundary. Do not
	// project that entire interval as a legacy ffmpeg phase: it makes the
	// timeline report a second owner for the same work.
	if err != nil {
		w.log.Error("clip.render.job.render_failed",
			zap.String("job_id", j.ID),
			zap.Int64("duration_ms", renderMS),
			zap.Error(err),
		)
		return nil, fmt.Errorf("clip.render: render plan: %w", err)
	}
	if outcome == nil {
		return nil, fmt.Errorf("clip.render: renderer returned a nil outcome")
	}
	// All post-render validation, probing, publication, metrics, and result
	// projection live in the ONE completion implementation shared with async
	// settle (worker_completion.go::completeRendered). There is no second,
	// inline completion body in this file: its removal is what keeps the submit
	// and settle halves from drifting apart.
	return w.completeRendered(ctx, j, tools, jobStart, req, inputs.prepared, plan, inputs.subtitles, publishFolderID, inputs.subtitleCompileMS, outcome, renderMS)
}
