package cliprender

// worker.go is the canonical Master job handler for clip.render.
//
// Pipeline:
//
//	decode payload → Normalize + Validate → parallel preparation
//	(Preparer) → compile ASS artifact (when subtitles enabled) →
//	compile + seal ClipRenderPlanV1 → single-pass Rust render_clip.
//
// The renderer and publisher remain mandatory at execution time: a plan
// that is only sealed or only rendered locally is never reported as a
// successful clip.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// ErrRenderPhaseNotImplemented is retained for the fail-closed case where a
// composition root exposes the job without attaching a render executor.
var ErrRenderPhaseNotImplemented = errors.New("clip.render: render phase not implemented yet (plan sealed — render_clip lands in the follow-up step)")

// ErrInvalidJobPayload is the typed sentinel for an undecodable job payload.
// Terminal: retrying the same payload can never succeed.
var ErrInvalidJobPayload = errors.New("clip.render: invalid job payload")

// Worker is the canonical clip.render job handler. It is constructed with
// the Preparer and bound to the Master via
// job.Service.RegisterHandler(TypeClipRender, job.HandlerFunc(worker.Handle)).
type Worker struct {
	preparer     *Preparer
	workspaceDir string
	subtitles    SubtitleCompiler // optional until the ASS-compiler step wires it
	renderer     RenderExecutor   // optional until the render-phase step consumes it
	publisher    RenderPublisher  // optional in unit tests; required by production wiring
	log          *zap.Logger
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

// WithSubtitleCompiler attaches the canonical ASS compiler. Optional: when
// subtitles are disabled no compiler is needed; when enabled and nil, the
// worker fails closed with ErrSubtitleCompileUnavailable (never a plan
// without its ASS artifact).
func (w *Worker) WithSubtitleCompiler(c SubtitleCompiler) *Worker {
	if w != nil {
		w.subtitles = c
	}
	return w
}

// WithRenderExecutor attaches the Rust render_clip boundary. A missing
// executor remains a typed failure; a sealed plan is never reported as a
// rendered clip.
func (w *Worker) WithRenderExecutor(r RenderExecutor) *Worker {
	if w != nil {
		w.renderer = r
	}
	return w
}

// WithRenderPublisher attaches the canonical Drive publication + SQLite
// commit boundary. Production composition must wire it before exposing the
// route; tests may omit it when exercising preparation only.
func (w *Worker) WithRenderPublisher(p RenderPublisher) *Worker {
	if w != nil {
		w.publisher = p
	}
	return w
}

// Handle is the job.Handler-shaped entry point bound to the Master.
func (w *Worker) Handle(ctx context.Context, j *job.Job, tools *job.JobExecutionTools) (job.Result, error) {
	progress := safeProgress(tools)
	emit := safeEvent(tools)

	progress(0, "clip.render started")

	var req RenderRequest
	if err := json.Unmarshal(j.Payload, &req); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJobPayload, err)
	}
	req.Normalize()
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJobPayload, err)
	}

	w.log.Info("clip.render.job.start",
		zap.String("job_id", j.ID),
		zap.String("source_asset_id", req.SourceAssetID),
	)
	progress(10, "request validated; running parallel preparation")

	prepared, err := w.preparer.Prepare(ctx, &req, j.ID)
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

	// ── ASS artifact (subtitles enabled) ────────────────────────────────
	runDir := filepath.Join(w.workspaceDir, "runs", j.ID)
	var subtitleArtifact *SubtitleArtifact
	if req.Subtitles.Enabled {
		if w.subtitles == nil {
			return nil, fmt.Errorf("%w: subtitles.enabled=true but no SubtitleCompiler is wired (the ASS-compiler step wires the canonical materializer)", ErrSubtitleCompileUnavailable)
		}
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
		if err != nil {
			return nil, fmt.Errorf("clip.render: compile subtitles: %w", err)
		}
		emit("clip.render.subtitles.compiled", "ASS artifact compiled", map[string]any{
			"path":      subtitleArtifact.LocalPath,
			"sha256":    subtitleArtifact.SHA256,
			"mode":      subtitleArtifact.Mode,
			"cue_count": len(prepared.Transcript.Cues),
		})
	}

	// ── Seal the fully-resolved plan ───────────────────────────────────
	plan, err := Compile(CompileInput{
		RunID:          j.ID,
		Source:         prepared.Source,
		Watermark:      prepared.Watermark,
		WatermarkSpec:  req.Watermark,
		Background:     prepared.Background,
		BackgroundMode: req.Background.Mode,
		Subtitles:      subtitleArtifact,
		Contract:       prepared.Contract,
		AudioMode:      req.Audio.Mode,
		OutputPath:     filepath.Join(runDir, "rendered-clip.mp4"),
	})
	if err != nil {
		return nil, fmt.Errorf("clip.render: compile plan: %w", err)
	}

	emit("clip.render.plan.sealed", "ClipRenderPlanV1 sealed — fully resolved before Rust", map[string]any{
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

	progress(90, "plan sealed; rendering with Rust")
	outcome, err := w.renderer.Render(ctx, plan)
	if err != nil {
		return nil, fmt.Errorf("clip.render: render plan: %w", err)
	}
	if outcome == nil || outcome.OutputPath == "" || outcome.SizeBytes <= 0 {
		return nil, fmt.Errorf("clip.render: renderer returned an invalid output")
	}
	if w.publisher == nil {
		return nil, fmt.Errorf("clip.render: render publisher is not wired")
	}
	publication, err := w.publisher.Publish(ctx, RenderPublishInput{
		RunID:         j.ID,
		SourceAssetID: req.SourceAssetID,
		OutputPath:    outcome.OutputPath,
		Outcome:       outcome,
		Contract:      prepared.Contract,
		Transcript:    prepared.Transcript,
		Subtitles:     subtitleArtifact,
		DriveFolderID: req.Destination.DriveFolderID,
	})
	if err != nil {
		return nil, fmt.Errorf("clip.render: publish result: %w", err)
	}
	if publication == nil || publication.AssetID == "" || publication.DriveFileID == "" {
		return nil, fmt.Errorf("clip.render: publisher returned an invalid publication")
	}
	emit("clip.render.completed", "Rust render_clip completed", map[string]any{
		"output_path":  outcome.OutputPath,
		"size_bytes":   outcome.SizeBytes,
		"duration_sec": outcome.DurationSec,
		"ffmpeg_ms":    outcome.FFmpegMS,
	})
	progress(100, "clip.render completed")

	return renderedResult(j, &req, prepared, plan, subtitleArtifact, outcome, publication), nil
}

// renderedResult projects the *Prepared + sealed plan + render outcome +
// published derived asset into the canonical job result map. Only JSON-safe
// values — the result envelope is persisted by the Master.
func renderedResult(j *job.Job, req *RenderRequest, prepared *Prepared, plan ClipRenderPlanV1, subtitleArtifact *SubtitleArtifact, outcome *RenderOutcome, published *RenderPublishResult) job.Result {
	result := job.Result{
		"job_id":          j.ID,
		"source_asset_id": req.SourceAssetID,
		"phase":           "rendered",
		"transcript_mode": req.Transcript.Mode,
		"contract_id":     prepared.Contract.ContractID,
		"contract": map[string]any{
			"width":        prepared.Contract.Width,
			"height":       prepared.Contract.Height,
			"fps":          prepared.Contract.FPS,
			"video_codec":  prepared.Contract.VideoCodec,
			"audio_codec":  prepared.Contract.AudioCodec,
			"pixel_format": prepared.Contract.PixelFormat,
		},
		"plan": map[string]any{
			"version":     plan.Version,
			"plan_sha256": plan.PlanSHA256,
			"output_path": plan.OutputPath,
			"audio_mode":  plan.Audio.Mode,
			"background":  plan.Background.Mode,
		},
		"source": map[string]any{
			"asset_id":   prepared.Source.AssetID,
			"path":       prepared.Source.LocalPath,
			"sha256":     prepared.Source.SHA256,
			"size_bytes": prepared.Source.SizeBytes,
			"from_cache": prepared.Source.FromCache,
		},
		"transcript": map[string]any{
			"language":            prepared.Transcript.Language,
			"reused":              prepared.Transcript.Reused,
			"text_sha256":         prepared.Transcript.TextSHA256,
			"cues":                len(prepared.Transcript.Cues),
			"source_audio_sha256": prepared.Transcript.SourceAudioSHA256,
		},
		"timings": map[string]any{
			"total_wall_ms": prepared.Timings.TotalWallMS,
			"total_work_ms": prepared.Timings.TotalWorkMS,
			"parallel":      prepared.Timings.Parallel,
		},
	}
	if prepared.Watermark != nil {
		result["watermark"] = map[string]any{
			"asset_id": prepared.Watermark.AssetID,
			"path":     prepared.Watermark.LocalPath,
			"sha256":   prepared.Watermark.SHA256,
		}
	}
	if prepared.Background != nil {
		result["background"] = map[string]any{
			"asset_id": prepared.Background.AssetID,
			"path":     prepared.Background.LocalPath,
			"sha256":   prepared.Background.SHA256,
		}
	}
	if subtitleArtifact != nil {
		result["subtitles"] = map[string]any{
			"path":   subtitleArtifact.LocalPath,
			"sha256": subtitleArtifact.SHA256,
			"mode":   subtitleArtifact.Mode,
		}
	}
	if outcome != nil {
		result["render"] = map[string]any{
			"output_path":         outcome.OutputPath,
			"size_bytes":          outcome.SizeBytes,
			"duration_sec":        outcome.DurationSec,
			"width":               outcome.Width,
			"height":              outcome.Height,
			"fps":                 outcome.FPS,
			"ffmpeg_ms":           outcome.FFmpegMS,
			"audio_copy_eligible": outcome.AudioCopyEligible,
			"audio_encode_passes": outcome.AudioEncodePasses,
			"subtitle_raster_cpu": outcome.SubtitleRasterCPU,
		}
	}
	if published != nil {
		result["asset"] = map[string]any{
			"asset_id":      published.AssetID,
			"drive_file_id": published.DriveFileID,
			"drive_link":    published.DriveLink,
			"size_bytes":    published.SizeBytes,
			"sidecar_link":  published.SidecarLink,
		}
	}
	return result
}

// safeProgress returns a nil-safe progress callback.
func safeProgress(tools *job.JobExecutionTools) func(int, string) {
	return func(progress int, message string) {
		if tools != nil && tools.Progress != nil {
			tools.Progress(progress, message)
		}
	}
}

// safeEvent returns a nil-safe event callback.
func safeEvent(tools *job.JobExecutionTools) func(string, string, map[string]any) {
	return func(eventType, message string, data map[string]any) {
		if tools != nil && tools.Event != nil {
			tools.Event(eventType, message, data)
		}
	}
}
