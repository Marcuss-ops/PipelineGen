package cliprender

// worker.go is the canonical Master job handler for clip.render.
//
// Pipeline:
//
//	decode payload → Normalize + Validate → parallel preparation
//	(Preparer) → compile ASS artifact (when subtitles enabled) →
//	compile + seal ClipRenderPlanV1 → RenderingGen/Chronon render.
//
// When async completion is enabled, the sealed-plan boundary submits the
// RenderingGen job, persists a CAS continuation and releases the Master slot.
// The settle continuation resumes directly at the remote-render boundary and
// reuses the single completion tail in worker_completion.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

var ErrRenderPhaseNotImplemented = errors.New("clip.render: render phase not implemented yet (plan sealed — render_clip lands in the follow-up step)")
var ErrInvalidJobPayload = errors.New("clip.render: invalid job payload")

// Worker is the canonical clip.render job handler. Async completion is an
// opt-in composition concern; when disabled, the historical blocking path is
// byte-compatible and still calls the exact same completion tail.
type Worker struct {
	preparer             *Preparer
	workspaceDir         string
	subtitles            SubtitleCompiler
	renderer             RenderExecutor
	publisher            RenderPublisher
	folderResolver       DestinationFolderResolver
	overlayResolver      OverlaySegmentResolver
	outputProber         OutputProber
	continuationStore    ContinuationStore
	continuationEnqueuer ContinuationEnqueuer
	asyncCompletion      bool
	log                  *zap.Logger
}

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
	w.log.Info("clip.render.job.phase",
		zap.String("subsystem", "clip_render_worker"),
		zap.String("phase", "start"),
		zap.String("job_id", j.ID),
	)

	phase, err := parseRenderPhasePayload(j.Payload)
	if err != nil {
		return nil, err
	}
	if phase == RenderPhaseSettle {
		return w.handleAsyncSettle(ctx, j, tools, jobStart)
	}

	var req RenderRequest
	if err := json.Unmarshal(j.Payload, &req); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJobPayload, err)
	}
	req.Normalize()
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJobPayload, err)
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

	publishFolderID := req.Destination.DriveFolderID
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
		w.log.Info("clip.render.job.destination_resolved",
			zap.String("subsystem", "clip_render_worker"),
			zap.String("job_id", j.ID),
			zap.String("root_folder_id", req.Destination.DriveFolderID),
			zap.String("subfolder_name", req.Destination.SubfolderName),
			zap.String("resolved_folder_id", resolvedID),
		)
	}
	progress(10, "request validated; running parallel preparation")

	prepareStart := time.Now()
	prepared, err := w.preparer.Prepare(ctx, &req, j.ID)
	prepareEnd := time.Now()
	kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: StageClipPrepare}, prepareStart, prepareEnd, err)
	kernobs.RecordClipPhase(ctx, kernobs.ClipPhasePrepare, prepareStart, prepareEnd, kernobs.StageStatusCompleted, err)
	if err != nil {
		w.log.Error("clip.render.job.prepare_failed",
			zap.String("job_id", j.ID),
			zap.String("source_asset_id", req.SourceAssetID),
			zap.Error(err),
		)
		emit("clip.render.prepare.failed", "parallel preparation failed", map[string]any{
			"source_asset_id": req.SourceAssetID,
			"error":           err.Error(),
		})
		return nil, fmt.Errorf("clip.render: prepare: %w", err)
	}

	runDir := filepath.Join(w.workspaceDir, "runs", j.ID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return nil, fmt.Errorf("clip.render: create run directory: %w", err)
	}
	var subtitleArtifact *SubtitleArtifact
	subtitleCompileMS := int64(-1)
	if req.Subtitles.Enabled {
		if w.subtitles == nil {
			return nil, fmt.Errorf("%w: subtitles.enabled=true but no SubtitleCompiler is wired (the ASS-compiler step wires the canonical materializer)", ErrSubtitleCompileUnavailable)
		}
		subtitleCompileStart := time.Now()
		subtitleArtifact, err = w.subtitles.Compile(ctx, SubtitleCompileInput{
			RunID:          j.ID,
			AssetID:        req.SourceAssetID,
			Language:       prepared.Transcript.Language,
			Mode:           req.Subtitles.Mode,
			StyleID:        req.Subtitles.StyleID,
			Cues:           prepared.Transcript.Cues,
			ClipDurationMS: prepared.Source.DurationMS,
			SourceSHA256:   prepared.Source.SHA256,
			OutputDir:      runDir,
		})
		kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: StageClipSubtitles}, subtitleCompileStart, time.Now(), err)
		if err != nil {
			return nil, fmt.Errorf("clip.render: compile subtitles: %w", err)
		}
		subtitleCompileMS = time.Since(subtitleCompileStart).Milliseconds()
		emit("clip.render.subtitles.compiled", "ASS artifact compiled", map[string]any{
			"path":      subtitleArtifact.LocalPath,
			"sha256":    subtitleArtifact.SHA256,
			"mode":      subtitleArtifact.Mode,
			"cue_count": len(prepared.Transcript.Cues),
		})
	}

	var overlayInput *PlanOverlayInput
	if req.Overlay != nil {
		if w.overlayResolver == nil {
			return nil, fmt.Errorf("clip.render: overlay declared but no OverlaySegmentResolver is wired (single-pass overlay compositing not configured)")
		}
		segment, resolveErr := w.overlayResolver.Resolve(ctx, OverlayResolveInput{
			RenderJobID: req.Overlay.RenderJobID,
			RenderKey:   req.Overlay.RenderKey,
		})
		if resolveErr != nil {
			return nil, fmt.Errorf("clip.render: resolve overlay segment: %w", resolveErr)
		}
		if segment == nil || segment.LocalPath == "" || segment.SHA256 == "" {
			return nil, fmt.Errorf("clip.render: overlay resolver returned an invalid segment")
		}
		overlayInput = &PlanOverlayInput{
			Segment: segment,
			StartMS: (req.Overlay.StartUS + 500) / 1000,
			EndMS:   (req.Overlay.EndUS + 500) / 1000,
		}
		emit("clip.render.overlay.single_pass", "overlay composited inside the Chronon render pass (single encode)", map[string]any{
			"render_job_id": req.Overlay.RenderJobID,
			"render_key":    req.Overlay.RenderKey,
			"sha256":        segment.SHA256,
			"start_ms":      overlayInput.StartMS,
			"end_ms":        overlayInput.EndMS,
		})
	}

	var watermarkSpec *WatermarkSpec
	if req.Watermark != nil && req.Watermark.Enabled {
		watermarkSpec = req.Watermark
	}
	plan, err := Compile(CompileInput{
		RunID:                  j.ID,
		Source:                 prepared.Source,
		DurationMS:             prepared.Source.DurationMS,
		Watermark:              prepared.Watermark,
		WatermarkSpec:          watermarkSpec,
		Background:             prepared.Background,
		BackgroundMode:         req.Background.Mode,
		Subtitles:              subtitleArtifact,
		SubtitlesStyle:         req.Subtitles.Style,
		Cues:                   prepared.Transcript.Cues,
		Contract:               prepared.Contract,
		AudioMode:              req.Audio.Mode,
		Overlay:                overlayInput,
		OutputPath:             filepath.Join(runDir, "rendered-clip.mp4"),
		ForegroundScalePercent: req.Output.ForegroundScalePercent,
	})
	if err != nil {
		return nil, fmt.Errorf("clip.render: compile plan: %w", err)
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
		return result, fmt.Errorf("%w: job_id=%s source_asset_id=%s plan_sha256=%s", ErrRenderPhaseNotImplemented, j.ID, req.SourceAssetID, plan.PlanSHA256)
	}

	if w.asyncCompletion {
		return w.handleAsyncSubmit(ctx, j, tools, jobStart, &req, prepared, plan, subtitleArtifact, publishFolderID, subtitleCompileMS)
	}

	progress(90, "plan sealed; rendering with Chronon")
	renderSlotStart := time.Now()
	renderStart := time.Now()
	w.log.Info("clip.render.job.phase",
		zap.String("subsystem", "clip_render_worker"),
		zap.String("phase", "render_start"),
		zap.String("job_id", j.ID),
		zap.String("plan_sha256", plan.PlanSHA256),
		zap.String("output_path", plan.OutputPath),
	)
	outcome, err := func() (o *RenderOutcome, e error) {
		e = kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
			Stage:     StageClipRender,
			Component: kernobs.ComponentName("chronon"),
			Operation: kernobs.OperationName("render_clip"),
		}, func(opCtx context.Context) error {
			var rErr error
			o, rErr = w.renderer.Render(opCtx, plan)
			return rErr
		})
		return o, e
	}()
	renderEnd := time.Now()
	renderMS := renderEnd.Sub(renderStart).Milliseconds()
	kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseRenderSlot, renderSlotStart, renderEnd, kernobs.StageStatusCompleted, nil)
	kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: StageClipRender}, renderStart, renderEnd, err)
	kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseFFmpeg, renderStart, renderEnd, kernobs.StageStatusCompleted, err)
	if err != nil {
		w.log.Error("clip.render.job.render_failed",
			zap.String("job_id", j.ID),
			zap.Int64("duration_ms", renderMS),
			zap.Error(err),
		)
		return nil, fmt.Errorf("clip.render: render plan: %w", err)
	}
	if outcome != nil {
		w.log.Info("clip.render.job.phase",
			zap.String("subsystem", "clip_render_worker"),
			zap.String("phase", "render_done"),
			zap.String("job_id", j.ID),
			zap.String("backend", string(outcome.Backend)),
			zap.Int64("duration_ms", renderMS),
			zap.Int64("ffmpeg_ms", outcome.FFmpegMS),
			zap.Int64("size_bytes", outcome.SizeBytes),
			zap.String("output_path", outcome.OutputPath),
		)
	}
	return w.completeRendered(ctx, j, tools, jobStart, &req, prepared, plan, subtitleArtifact, publishFolderID, subtitleCompileMS, outcome, renderMS)
}
