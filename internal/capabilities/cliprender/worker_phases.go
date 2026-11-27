package cliprender

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
	// fromCheckpoint records that preparation was NOT re-run on this attempt:
	// the sealed plan and its preparation results were loaded from the durable
	// job → resume index. It is projected into the submit result so the saving
	// is observable instead of inferred (CRA-07).
	fromCheckpoint bool
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

// loadSubmitCheckpoint resolves a previously sealed plan for THIS job, so a
// broker retry after the seal can skip resolution/materialization/compilation.
//
// Fail-open by design: a missing index, a corrupt entry, a drifted document or
// a different request all mean "prepare again", never "fail the render". The
// index is an accelerator; correctness still comes from the sealed plan and
// the content-addressed resume document.
func (w *Worker) loadSubmitCheckpoint(ctx context.Context, j *job.Job, req *RenderRequest) (*phaseInputs, bool) {
	store, ok := w.continuationStore.(SubmitCheckpointStore)
	if !ok || w.continuationStore == nil || j == nil || req == nil {
		return nil, false
	}
	ref, found, err := store.GetSubmitCheckpoint(ctx, j.ID)
	if err != nil {
		w.log.Warn("clip.render.submit_checkpoint_unreadable",
			zap.String("job_id", j.ID), zap.Error(err))
		return nil, false
	}
	if !found {
		return nil, false
	}
	doc, err := w.continuationStore.GetResumeDocument(ctx, ref)
	if err != nil {
		w.log.Warn("clip.render.submit_checkpoint_invalid",
			zap.String("job_id", j.ID), zap.Error(err))
		return nil, false
	}
	if doc.Plan.RunID != j.ID {
		w.log.Warn("clip.render.submit_checkpoint_foreign_plan",
			zap.String("job_id", j.ID), zap.String("plan_run_id", doc.Plan.RunID))
		return nil, false
	}
	docFP, docErr := doc.Request.Fingerprint()
	reqFP, reqErr := req.Fingerprint()
	if docErr != nil || reqErr != nil || docFP != reqFP {
		w.log.Warn("clip.render.submit_checkpoint_request_drift",
			zap.String("job_id", j.ID), zap.String("checkpoint_fingerprint", digestPrefix(docFP)))
		return nil, false
	}
	inputs := &phaseInputs{
		request:           *req,
		prepared:          preparedFromResume(doc),
		plan:              doc.Plan,
		subtitles:         doc.Subtitles,
		resumeFolder:      doc.PublishFolderID,
		subtitleCompileMS: -1,
		fromCheckpoint:    true,
	}
	if doc.SubtitleCompileMS != nil {
		inputs.subtitleCompileMS = *doc.SubtitleCompileMS
	}
	return inputs, true
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
	// The key proves the request is identical; the input digest proves the
	// BYTES behind its asset ids still are (CRA-03). A record from before the
	// digest existed ("") is not certified either.
	if !cacheInputMatches(ctx, w.inputRevisions, req, rec) {
		w.log.Info("clip.render cache entry stale: input revision changed",
			zap.String("job_id", j.ID),
			zap.String("fingerprint", digestPrefix(fp)),
			zap.String("asset_id", rec.AssetID))
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
	// never derives a value from an uninstrumented input. A cache hit in the
	// submit phase has no continuation yet, so the request acceptance is this
	// job's own durable creation time.
	completedAt := time.Now()
	acceptedAt := time.Time{}
	if j != nil {
		acceptedAt = j.CreatedAt
	}
	finalizeMetrics(cached.Metrics, completedAt.Sub(jobStart).Milliseconds(), cached.DurationSec)
	applyRequestTimings(cached.Metrics, acceptedAt, time.Time{}, jobStart, completedAt, rec.DeliveryStatus)
	res["cache_hit"] = true
	res["fingerprint"] = fp
	res["phase"] = "cached"
	res["delivery_status"] = rec.DeliveryStatus
	res["drive_file_id"] = rec.DriveFileID
	addQueueWaitMS(res, j, jobStart)
	progress(100, "clip.render cache hit")
	return res, true
}

// tryRenderReuse is the CRA-04 delivery-only fast path: the full (destination-
// aware) fingerprint missed, but another destination already certified the SAME
// render. The artifact is published to the requested folder from its durable
// locator and the new destination gets its OWN cache row; no preparation, no
// GPU, no second render.
//
// Pre-condition failures deliberately fall back to a full render: a cache port
// without the destination-independent lookup, an uncertified input digest, a
// changed input revision, sidecar subtitles (the bundle is not in the cache
// record) and an artifact with no locator to stream from. A PUBLISH failure is
// different and is returned: the delivery may already be partially committed,
// so re-rendering would duplicate it.
func (w *Worker) tryRenderReuse(ctx context.Context, j *job.Job, req *RenderRequest, publishFolderID string, jobStart time.Time, progress func(int, string)) (job.Result, bool, error) {
	if w.renderCache == nil || w.publisher == nil || req == nil {
		return nil, false, nil
	}
	if req.Subtitles != nil && req.Subtitles.Enabled && req.Subtitles.Mode == SubtitlesModeSidecar {
		return nil, false, nil
	}
	byRender, ok := w.renderCache.(RenderCacheByRender)
	if !ok {
		return nil, false, nil
	}
	renderFP, err := req.RenderFingerprint()
	if err != nil {
		return nil, false, nil
	}
	rec, err := byRender.GetByRender(ctx, renderFP)
	if err != nil || rec == nil {
		return nil, false, nil
	}
	if rec.ArtifactURL == "" && rec.StorageKey == "" {
		return nil, false, nil
	}
	// A reused artifact has no fresh render behind it to prove the inputs: the
	// digest recorded at publish time is the ONLY certification available.
	if strings.TrimSpace(rec.InputSHA256) == "" {
		return nil, false, nil
	}
	if !cacheInputMatches(ctx, w.inputRevisions, req, rec) {
		w.log.Info("clip.render reuse rejected: input revision changed",
			zap.String("job_id", j.ID), zap.String("render_fingerprint", digestPrefix(renderFP)))
		return nil, false, nil
	}
	// An empty title+language would make the publisher commit an empty
	// search_text, which the index worker dead-letters. Fall back to a render
	// (whose preparation owns the real title) instead of poisoning the index.
	if strings.TrimSpace(rec.SourceTitle) == "" && strings.TrimSpace(rec.Language) == "" {
		return nil, false, nil
	}

	outcome := &RenderOutcome{
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
	if outcome.Metrics != nil {
		outcome.Metrics.BackendSelected = rec.Backend
	}
	progress(20, "render already certified; delivering to the requested destination")
	publication, pubErr := w.publisher.Publish(ctx, RenderPublishInput{
		RunID:               j.ID,
		SourceAssetID:       req.SourceAssetID,
		SourceTitle:         rec.SourceTitle,
		DriveFolderID:       publishFolderID,
		Outcome:             outcome,
		CertifiedSHA256:     rec.SHA256,
		CertifiedSizeBytes:  rec.SizeBytes,
		ArtifactURL:         rec.ArtifactURL,
		ArtifactStorageKey:  rec.StorageKey,
		ArtifactContentType: rec.ContentType,
		Transcript:          &TranscriptResult{Language: rec.Language},
	})
	if pubErr != nil {
		// The artifact may already be committed: re-rendering would duplicate the
		// delivery. Surface the failure so the job retries the delivery instead.
		return nil, false, fmt.Errorf("clip.render: deliver reused render to %q: %w", publishFolderID, pubErr)
	}

	// Independent delivery row: the new destination gets its own full-fingerprint
	// cache entry, so a later hit for B returns B's locator and a re-delivery to
	// A cannot touch it.
	if fp, fpErr := req.Fingerprint(); fpErr == nil {
		recB := &RenderCacheRecord{
			Fingerprint:       fp,
			RenderFingerprint: renderFP,
			InputSHA256:       rec.InputSHA256,
			SourceTitle:       rec.SourceTitle,
			Language:          rec.Language,
			AssetID:           publication.AssetID,
			StorageKey:        outcome.StorageKey,
			ArtifactURL:       outcome.ArtifactURL,
			ContentType:       outcome.ContentType,
			SHA256:            outcome.SHA256,
			SizeBytes:         outcome.SizeBytes,
			DurationSec:       outcome.DurationSec,
			Width:             outcome.Width,
			Height:            outcome.Height,
			FPSNum:            outcome.FPSNum,
			FPSDen:            outcome.FPSDen,
			Backend:           outcome.Backend,
			DeliveryStatus:    publication.DeliveryStatus,
			DriveFileID:       publication.DriveFileID,
		}
		if putErr := w.renderCache.Put(ctx, recB); putErr != nil {
			w.log.Warn("clip.render cache put failed for reused delivery",
				zap.String("fingerprint", digestPrefix(fp)), zap.Error(putErr))
		}
	}

	synthPrepared := &Prepared{
		RunID:      j.ID,
		Source:     &MaterializedAsset{AssetID: req.SourceAssetID, Title: rec.SourceTitle, SHA256: rec.SHA256},
		Transcript: &TranscriptResult{Language: rec.Language},
	}
	res := renderedResult(j, req, synthPrepared, ClipRenderPlanV1{}, nil, outcome, publication)
	completedAt := time.Now()
	acceptedAt := time.Time{}
	if j != nil {
		acceptedAt = j.CreatedAt
	}
	finalizeMetrics(outcome.Metrics, completedAt.Sub(jobStart).Milliseconds(), outcome.DurationSec)
	applyRequestTimings(outcome.Metrics, acceptedAt, time.Time{}, jobStart, completedAt, publication.DeliveryStatus)
	res["cache_hit"] = true
	res["render_reused"] = true
	res["reused_from_asset_id"] = rec.AssetID
	res["render_fingerprint"] = renderFP
	res["fingerprint"] = reqFingerprintOrEmpty(req)
	res["phase"] = "delivery_reuse"
	res["delivery_status"] = publication.DeliveryStatus
	res["drive_file_id"] = publication.DriveFileID
	addQueueWaitMS(res, j, jobStart)
	w.log.Info("clip.render render reused for new destination",
		zap.String("job_id", j.ID),
		zap.String("render_fingerprint", digestPrefix(renderFP)),
		zap.String("source_asset_id", rec.AssetID),
		zap.String("published_asset_id", publication.AssetID),
		zap.String("drive_folder_id", publishFolderID))
	progress(100, "clip.render delivered from certified cache")
	return res, true, nil
}

func reqFingerprintOrEmpty(req *RenderRequest) string {
	if req == nil {
		return ""
	}
	fp, err := req.Fingerprint()
	if err != nil {
		return ""
	}
	return fp
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
	// Index the resume address by job id BEFORE the remote submit, so a retry
	// caused by a failed submit can reuse this sealed plan. The write is
	// best-effort: losing the accelerator must not fail a render.
	checkpointStore, checkpointed := w.continuationStore.(SubmitCheckpointStore)
	if checkpointed {
		if cpErr := checkpointStore.PutSubmitCheckpoint(ctx, j.ID, resumeRef); cpErr != nil {
			w.log.Warn("clip.render.submit_checkpoint_write_failed",
				zap.String("job_id", j.ID), zap.Error(cpErr))
			checkpointed = false
		}
	}
	submission := Submission{
		RenderJobID:   remoteRenderID,
		PlanSHA256:    plan.PlanSHA256,
		CorrelationID: j.CorrelationID,
		State:         RemoteRenderSubmitted,
		Attempt:       1,
	}
	// The submit job's durable creation is the request acceptance. Carrying it
	// into the continuation is what lets the settle report measure
	// request_to_artifact_ms across the job split (CRA-01).
	if !j.CreatedAt.IsZero() {
		accepted := j.CreatedAt
		submission.AcceptedAt = &accepted
	}
	if submitErr := w.renderer.Submit(ctx, plan); submitErr != nil {
		return nil, fmt.Errorf("clip.render: submit remote render: %w", submitErr)
	}
	submitted := time.Now().UTC()
	submission.SubmittedAt = &submitted
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
	result["prepared_from_checkpoint"] = inputs.fromCheckpoint
	// The index has served its purpose once the settle continuation is
	// durable: a retry from here on would re-address the same submission.
	if checkpointed {
		if delErr := checkpointStore.DeleteSubmitCheckpoint(ctx, j.ID); delErr != nil {
			w.log.Debug("clip.render.submit_checkpoint_delete_failed",
				zap.String("job_id", j.ID), zap.Error(delErr))
		}
	}
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
		// The attempt budget bounds lane occupancy; it is NOT how long a finished
		// render should wait to be collected. Re-dispatch after the shorter
		// retry delay so a render that ends just past the budget is picked up on
		// the next attempt instead of waiting another full budget (CRA-08).
		// A zero budget cannot reach here (a budget of zero waits to
		// completion), so the delay is always positive.
		delay := waitBudget
		if w.settleRetry > 0 && w.settleRetry < delay {
			delay = w.settleRetry
		}
		deferrals := w.settleDeferrals.Add(1)
		addedLatencyMS := w.settleDeferredMS.Add(delay.Milliseconds())
		w.log.Info("clip.render.job.settle_deferred",
			zap.String("subsystem", "clip_render_worker"),
			zap.String("job_id", j.ID),
			zap.String("render_job_id", continuation.Submission.RenderJobID),
			zap.Duration("wait_budget", waitBudget),
			zap.Duration("deferral_delay", delay),
			zap.Int64("deferrals_total", deferrals),
			zap.Int64("added_latency_ms_total", addedLatencyMS),
			zap.Int64("elapsed_ms", renderMS),
		)
		return nil, job.DeferredAfter(delay, fmt.Sprintf(
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
	acceptedAt, submittedAt := continuationTimings(continuation)
	return w.completeRendered(ctx, j, tools, jobStart, acceptedAt, submittedAt, req, inputs.prepared, plan, inputs.subtitles, publishFolderID, inputs.subtitleCompileMS, outcome, renderMS)
}
