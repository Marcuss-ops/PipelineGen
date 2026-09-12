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
	outputProber         OutputProber              // probes actual bytes for exact contract validation
	log                  *zap.Logger
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
	return &Worker{preparer: preparer, workspaceDir: workspaceDir, log: log}, nil
}

func (w *Worker) Handle(ctx context.Context, j *job.Job, tools *job.JobExecutionTools) (job.Result, error) {
	progress := safeProgress(tools)
	emit := safeEvent(tools)

	progress(0, "clip.render started")
	jobStart := time.Now()
	kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseSubmitted, jobStart, jobStart, kernobs.StageStatusCompleted, nil)
	kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseClaimed, jobStart, jobStart, kernobs.StageStatusCompleted, nil)
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
	var (
		req               RenderRequest
		prepared          *Prepared
		plan              ClipRenderPlanV1
		subtitleArtifact  *SubtitleArtifact
		publishFolderID   string
		subtitleCompileMS int64 = -1
	)
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
		req = doc.Request
		req.Normalize()
		if err := req.Validate(); err != nil {
			return nil, fmt.Errorf("%w: resume request: %v", ErrInvalidJobPayload, err)
		}
		plan = doc.Plan
		subtitleArtifact = doc.Subtitles
		publishFolderID = doc.PublishFolderID
		prepared = preparedFromResume(doc)
	} else {
		if err := json.Unmarshal(j.Payload, &req); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidJobPayload, err)
		}
		req.Normalize()
		if err := req.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidJobPayload, err)
		}
	}

	w.log.Info("clip.render.job.start",
		zap.String("subsystem", "clip_render_worker"),
		zap.String("job_id", j.ID),
		zap.String("source_asset_id", req.SourceAssetID),
		zap.String("destination_folder_id", req.Destination.DriveFolderID),
		zap.Bool("subtitles_enabled", req.Subtitles.Enabled),
		zap.Bool("watermark_requested", req.Watermark != nil),
		zap.Bool("background_mode", req.Background.Mode != ""),
		zap.Bool("overlay_requested", req.Overlay != nil),
		zap.Bool("require_gpu", req.Execution.RequireGPU),
		zap.Bool("require_zero_copy", req.Execution.RequireZeroCopy),
	)
	// ── Destination folder resolution (ONCE per job, never in the publisher) ──
	// When the request carries destination.subfolder_name (a script/batch
	// identity all its clips share), the worker resolves the leaf folder
	// create-or-reuse through the canonical DestinationFolderResolver and
	// hands the publisher the fully-resolved leaf ID. The publisher is dumb
	// by contract: it never creates folders. Clips of the same batch carry
	// the same subfolder_name, so each job resolves the same folder and they
	// converge on one shared Drive directory. Without a subfolder_name the
	// request's destination.drive_folder_id is already the resolved leaf and
	// is passed through verbatim.
	if phase == RenderPhaseSettle {
		// Destination resolution and all preparation already happened before
		// Submit. Repeating it here would re-download assets and can select a
		// different Drive folder after a restart.
		publishFolderID = continuationResumeFolder(publishFolderID, req.Destination.DriveFolderID)
	} else {
		publishFolderID = req.Destination.DriveFolderID
		if strings.TrimSpace(req.Destination.SubfolderName) != "" {
			if w.folderResolver == nil {
				return nil, fmt.Errorf("clip.render: destination.subfolder_name=%q declared but no DestinationFolderResolver is wired (the publisher must never create folders)", req.Destination.SubfolderName)
			}
			resolveStart := time.Now()
			resolvedID, resolveErr := w.folderResolver.ResolveDestinationFolder(ctx, DestinationFolderResolveInput{
				RootFolderID:  req.Destination.DriveFolderID,
				SubfolderName: req.Destination.SubfolderName,
			})
			kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: StageClipDestinationResolve}, resolveStart, time.Now(), resolveErr)
			if resolveErr != nil {
				return nil, fmt.Errorf("clip.render: resolve destination subfolder %q: %w", req.Destination.SubfolderName, resolveErr)
			}
			if strings.TrimSpace(resolvedID) == "" {
				return nil, fmt.Errorf("clip.render: destination subfolder %q resolved to an empty folder ID", req.Destination.SubfolderName)
			}
			publishFolderID = resolvedID
			w.log.Debug("clip.render.job.destination_resolved",
				zap.String("subsystem", "clip_render_worker"),
				zap.String("job_id", j.ID),
				zap.String("root_folder_id", req.Destination.DriveFolderID),
				zap.String("subfolder_name", req.Destination.SubfolderName),
				zap.String("resolved_folder_id", resolvedID),
			)
		}
	}
	if phase == RenderPhaseSettle {
		progress(90, "remote render submitted; settling certified artifact")
	} else {
		progress(10, "request validated; running parallel preparation")
		prepared, plan, subtitleArtifact, subtitleCompileMS, err = w.preparePlan(ctx, &req, j.ID, emit)
		if err != nil {
			return nil, err
		}
	}

	emit("clip.render.plan.sealed", "ClipRenderPlanV1 sealed — fully resolved before Chronon", map[string]any{
		"plan_version": plan.Version,
		"plan_sha256":  plan.PlanSHA256,
		"output_path":  plan.OutputPath,
		"source":       plan.Source.Path,
		"subtitles":    plan.Subtitles != nil,
		"watermark":    plan.Watermark != nil,
		"background":   plan.Background.Mode,
	})
	if w.renderer == nil {
		result := renderedResult(j, &req, prepared, plan, subtitleArtifact, nil, nil)
		result["phase"] = "plan_sealed"
		return result, fmt.Errorf(
			"%w: job_id=%s source_asset_id=%s plan_sha256=%s",
			ErrRenderPhaseNotImplemented, j.ID, req.SourceAssetID, plan.PlanSHA256)
	}
	if phase == RenderPhaseSubmit {
		asyncRenderer, asyncOK := w.renderer.(AsyncRenderExecutor)
		if asyncOK {
			if w.continuationStore == nil || w.continuationEnqueuer == nil {
				return nil, fmt.Errorf("clip.render: asynchronous executor is wired but continuation store/enqueuer is missing")
			}
			doc := ResumeDocument{
				Plan:            plan,
				Request:         req,
				PublishFolderID: publishFolderID,
				SourceTitle:     prepared.Source.Title,
				SourceSizeBytes: prepared.Source.SizeBytes,
				Contract:        prepared.Contract,
				Transcript:      prepared.Transcript,
				Subtitles:       subtitleArtifact,
			}
			resumeRef, storeErr := w.continuationStore.PutResumeDocument(ctx, doc)
			if storeErr != nil {
				return nil, fmt.Errorf("clip.render: persist continuation: %w", storeErr)
			}
			submission := Submission{
				RenderJobID:   plan.RunID,
				PlanSHA256:    plan.PlanSHA256,
				CorrelationID: j.CorrelationID,
				State:         RemoteRenderSubmitted,
				Attempt:       1,
			}
			if submitErr := asyncRenderer.Submit(ctx, plan); submitErr != nil {
				return nil, fmt.Errorf("clip.render: submit remote render: %w", submitErr)
			}
			continuation := Continuation{Submission: submission, Resume: resumeRef}
			childID, enqueueErr := w.continuationEnqueuer.EnqueueContinuation(ctx, ContinuationRequest{
				ParentJobID:  j.ID,
				ParentRunID:  j.ID,
				ActiveKey:    ActiveKeyFor(plan.RunID, submission.Attempt),
				Continuation: continuation,
			})
			if enqueueErr != nil {
				return nil, fmt.Errorf("clip.render: enqueue settle continuation: %w", enqueueErr)
			}
			result := renderedResult(j, &req, prepared, plan, subtitleArtifact, nil, nil)
			result["phase"] = "submitted"
			result["parent_state"] = ParentStateWaitingChildren
			result["child_job_id"] = childID
			result["render_submission"] = submission
			result["continuation"] = continuation
			progress(100, "remote render submitted; Master slot released")
			return result, nil
		}
	}
	if phase == RenderPhaseSettle {
		if _, ok := w.renderer.(AsyncRenderExecutor); !ok {
			return nil, fmt.Errorf("clip.render: settle phase requires an AsyncRenderExecutor")
		}
	}

	progress(90, "plan sealed; rendering with Chronon")
	// render_slot is the interval this Master job HOLDS its render slot. It is
	// measured from the renderer invocation to the materialized artifact, i.e.
	// the real occupancy the slot pool/gate bounds — never a zero-width marker
	// (the previous start==end form measured nothing and reported 0 ms of slot
	// wait for every render).
	renderSlotStart := time.Now()
	renderStart := time.Now()
	w.log.Debug("clip.render.job.phase",
		zap.String("subsystem", "clip_render_worker"),
		zap.String("phase", "render_start"),
		zap.String("job_id", j.ID),
		zap.String("plan_sha256", plan.PlanSHA256),
		zap.String("output_path", plan.OutputPath),
	)
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
			var rErr error
			if phase == RenderPhaseSettle {
				o, rErr = w.renderer.(AsyncRenderExecutor).Settle(opCtx, plan)
			} else {
				o, rErr = w.renderer.Render(opCtx, plan)
			}
			return rErr
		})
		return o, e
	}()
	renderEnd := time.Now()
	renderMS := renderEnd.Sub(renderStart).Milliseconds()
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
	// Keep all post-render validation, probing, publication, metrics, and
	// result projection in the single completion implementation shared with
	// async settle. The historical inline block below is retained only as a
	// source-compatible migration tail and is unreachable after this return.
	return w.completeRendered(ctx, j, tools, jobStart, &req, prepared, plan, subtitleArtifact, publishFolderID, subtitleCompileMS, outcome, renderMS)
}
