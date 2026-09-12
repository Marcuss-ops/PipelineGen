package cliprender

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

// WithAsyncCompletion enables the submit→settle boundary. The switch is
// deliberately composition-owned: production can keep the historical blocking
// path until the continuation worker is validated against the live queue.
// Fail-closed: enabling without both durable continuation dependencies is a
// configuration error surfaced immediately.
func (w *Worker) WithAsyncCompletion(store ContinuationStore, enqueuer ContinuationEnqueuer) error {
	if w == nil {
		return fmt.Errorf("clip.render async completion: worker is nil")
	}
	if store == nil {
		return fmt.Errorf("clip.render async completion: continuation store is required")
	}
	if enqueuer == nil {
		return fmt.Errorf("clip.render async completion: continuation enqueuer is required")
	}
	if _, ok := w.renderer.(AsyncRenderExecutor); !ok {
		return fmt.Errorf("clip.render async completion: render executor does not implement Submit/Settle")
	}
	w.continuationStore = store
	w.continuationEnqueuer = enqueuer
	w.asyncCompletion = true
	return nil
}

// parseRenderPhasePayload is the single decoder for phase dispatch. Normal
// RenderRequest payloads carry no render_phase and therefore remain submit.
func parseRenderPhasePayload(raw json.RawMessage) (RenderPhase, error) {
	if len(raw) == 0 {
		return RenderPhaseSubmit, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidJobPayload, err)
	}
	return ParseRenderPhase(payload)
}

type settlePayload struct {
	RenderPhase  RenderPhase     `json:"render_phase"`
	Continuation json.RawMessage `json:"continuation"`
	ParentJobID  string          `json:"parent_job_id,omitempty"`
	ParentRunID  string          `json:"parent_run_id,omitempty"`
}

// handleAsyncSubmit persists the resume document, submits the deterministic
// remote render, enqueues one idempotent settle child and RETURNS. No terminal
// wait, artifact download, probe or publication occurs on this Master slot.
func (w *Worker) handleAsyncSubmit(
	ctx context.Context,
	j *job.Job,
	tools *job.JobExecutionTools,
	jobStart time.Time,
	req *RenderRequest,
	prepared *Prepared,
	plan ClipRenderPlanV1,
	subtitles *SubtitleArtifact,
	publishFolderID string,
	subtitleCompileMS int64,
) (job.Result, error) {
	progress := safeProgress(tools)
	emit := safeEvent(tools)
	asyncExec, ok := w.renderer.(AsyncRenderExecutor)
	if !ok {
		return nil, fmt.Errorf("clip.render: async completion enabled but renderer has no Submit/Settle contract")
	}
	if w.continuationStore == nil || w.continuationEnqueuer == nil {
		return nil, fmt.Errorf("clip.render: async completion enabled without durable continuation wiring")
	}
	if prepared == nil || prepared.Contract == nil {
		return nil, fmt.Errorf("clip.render: async submit preparation snapshot is incomplete")
	}

	// Store BEFORE submit. An orphan CAS document is harmless and deduplicated;
	// the opposite ordering creates a dangerous crash gap where a remote render
	// exists but no durable local continuation can collect it.
	doc := ResumeDocument{
		Plan:            plan,
		Request:         *req,
		PublishFolderID: publishFolderID,
		Contract:        prepared.Contract,
		Transcript:      prepared.Transcript,
		Subtitles:       subtitles,
	}
	resumeRef, err := w.continuationStore.PutResumeDocument(ctx, doc)
	if err != nil {
		return nil, fmt.Errorf("clip.render: persist async resume document: %w", err)
	}

	submitStart := time.Now()
	if err := asyncExec.Submit(ctx, plan); err != nil {
		return nil, fmt.Errorf("clip.render: submit remote render: %w", err)
	}
	submitMS := time.Since(submitStart).Milliseconds()

	submission := Submission{
		RenderJobID:   plan.RunID,
		PlanSHA256:    plan.PlanSHA256,
		CorrelationID: j.CorrelationID,
		State:         RemoteRenderSubmitted,
		// The remote identity is deterministic for the parent job. Broker
		// retries must address the same settle child, so the continuation
		// attempt stays 1 for this render identity.
		Attempt: 1,
	}
	continuation := Continuation{Submission: submission, Resume: resumeRef}
	if err := continuation.Validate(); err != nil {
		return nil, err
	}
	parentRunID := j.RootJobID
	if strings.TrimSpace(parentRunID) == "" {
		parentRunID = j.ID
	}
	childID, err := w.continuationEnqueuer.EnqueueContinuation(ctx, ContinuationRequest{
		ParentJobID:  j.ID,
		ParentRunID:  parentRunID,
		ActiveKey:    ActiveKeyFor(plan.RunID, submission.Attempt),
		Continuation: continuation,
	})
	if err != nil {
		return nil, fmt.Errorf("clip.render: enqueue settle continuation: %w", err)
	}
	if strings.TrimSpace(childID) == "" {
		return nil, fmt.Errorf("clip.render: continuation enqueuer returned an empty child job id")
	}

	emit("clip.render.remote.submitted", "RenderingGen render accepted; Master slot released", map[string]any{
		"render_job_id": plan.RunID,
		"plan_sha256": plan.PlanSHA256,
		"settle_job_id": childID,
		"resume_sha256": resumeRef.SHA256,
		"remote_state": RemoteRenderSubmitted,
		"submit_ms": submitMS,
	})
	progress(100, "clip.render submitted; awaiting remote completion")
	w.log.Info("clip.render.job.submitted",
		zap.String("job_id", j.ID),
		zap.String("render_job_id", plan.RunID),
		zap.String("settle_job_id", childID),
		zap.String("plan_sha256", plan.PlanSHA256),
		zap.Int64("submit_ms", submitMS),
		zap.Int64("slot_held_ms", time.Since(jobStart).Milliseconds()),
	)

	return job.Result{
		"job_id":          j.ID,
		"phase":           string(RenderPhaseSubmit),
		"parent_state":    ParentStateWaitingChildren,
		"child_job_ids":   []string{childID},
		"render_job_id":   plan.RunID,
		"plan_sha256":     plan.PlanSHA256,
		"remote_state":    RemoteRenderSubmitted,
		"settle_job_id":   childID,
		"resume_sha256":   resumeRef.SHA256,
		"resume_size_bytes": resumeRef.SizeBytes,
		"submit_ms":       submitMS,
	}, nil
}

// handleAsyncSettle resumes directly from the sealed plan. It never runs the
// preparer, subtitle compiler, folder resolver or overlay resolver again.
func (w *Worker) handleAsyncSettle(ctx context.Context, j *job.Job, tools *job.JobExecutionTools, jobStart time.Time) (job.Result, error) {
	if !w.asyncCompletion {
		return nil, fmt.Errorf("clip.render: settle payload received while async completion is disabled")
	}
	asyncExec, ok := w.renderer.(AsyncRenderExecutor)
	if !ok {
		return nil, fmt.Errorf("clip.render: settle phase requires an AsyncRenderExecutor")
	}
	if w.continuationStore == nil {
		return nil, fmt.Errorf("clip.render: settle phase has no continuation store")
	}

	var payload settlePayload
	if err := json.Unmarshal(j.Payload, &payload); err != nil {
		return nil, fmt.Errorf("%w: decode settle payload: %v", ErrInvalidJobPayload, err)
	}
	if payload.RenderPhase != RenderPhaseSettle {
		return nil, fmt.Errorf("%w: settle handler received phase %q", ErrInvalidJobPayload, payload.RenderPhase)
	}
	continuation, err := DecodeContinuation(payload.Continuation)
	if err != nil {
		return nil, err
	}
	doc, err := w.continuationStore.GetResumeDocument(ctx, continuation.Resume)
	if err != nil {
		return nil, fmt.Errorf("clip.render: load async resume document: %w", err)
	}
	if err := doc.Attributes(continuation.Submission.RenderJobID, continuation.Submission.PlanSHA256); err != nil {
		return nil, err
	}
	if doc.Contract == nil {
		return nil, fmt.Errorf("clip.render: resume document is missing the resolved output contract")
	}

	progress := safeProgress(tools)
	emit := safeEvent(tools)
	progress(10, "resuming submitted RenderingGen render")
	emit("clip.render.remote.settling", "waiting for submitted RenderingGen render", map[string]any{
		"render_job_id": continuation.Submission.RenderJobID,
		"remote_state": RemoteRenderRendering,
	})

	renderSlotStart := time.Now()
	renderStart := renderSlotStart
	outcome, err := func() (o *RenderOutcome, e error) {
		e = kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
			Stage:     StageClipRender,
			Component: kernobs.ComponentName("chronon"),
			Operation: kernobs.OperationName("render_clip"),
		}, func(opCtx context.Context) error {
			var settleErr error
			o, settleErr = asyncExec.Settle(opCtx, doc.Plan)
			return settleErr
		})
		return o, e
	}()
	renderEnd := time.Now()
	renderMS := renderEnd.Sub(renderStart).Milliseconds()
	kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseRenderSlot, renderSlotStart, renderEnd, kernobs.StageStatusCompleted, err)
	kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: StageClipRender}, renderStart, renderEnd, err)
	kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseFFmpeg, renderStart, renderEnd, kernobs.StageStatusCompleted, err)
	if err != nil {
		return nil, fmt.Errorf("clip.render: settle remote render: %w", err)
	}

	// ResumeDocument v1 predates the full Prepared snapshot. Completion only
	// needs the resolved contract/transcript and source identity; no asset is
	// re-resolved or re-downloaded. Source title falls back deterministically to
	// the asset id until the resume contract is extended additively.
	prepared := &Prepared{
		RunID: doc.Plan.RunID,
		Source: &MaterializedAsset{
			AssetID:    doc.Request.SourceAssetID,
			Title:      doc.Request.SourceAssetID,
			LocalPath:  doc.Plan.Source.Path,
			SHA256:     doc.Plan.Source.SHA256,
			DurationMS: doc.Plan.DurationMS,
		},
		Transcript: doc.Transcript,
		Contract:   doc.Contract,
	}
	result, err := w.completeRendered(ctx, j, tools, jobStart, &doc.Request, prepared, doc.Plan, doc.Subtitles, doc.PublishFolderID, -1, outcome, renderMS)
	if err != nil {
		return nil, err
	}
	result["phase"] = string(RenderPhaseSettle)
	result["remote_state"] = RemoteRenderCompleted
	if payload.ParentJobID != "" {
		result["parent_job_id"] = payload.ParentJobID
	}
	result["settle_job_id"] = j.ID
	return result, nil
}
