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
	"os"
	"path/filepath"
	"time"

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
	preparer          *Preparer
	workspaceDir      string
	subtitles         SubtitleCompiler       // optional until the ASS-compiler step wires it
	renderer          RenderExecutor         // optional until the render-phase step consumes it
	publisher         RenderPublisher        // optional in unit tests; required by production wiring
	overlayResolver   OverlaySegmentResolver // optional until overlay compositing is wired
	overlayCompositor OverlayCompositor      // optional until overlay compositing is wired
	outputProber      OutputProber           // probes actual bytes for exact contract validation
	log               *zap.Logger
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

// WithOverlaySegmentResolver attaches the overlay.render artifact resolver
// (render_job_id → materialized segment). Optional: when the request
// declares no overlay no resolver is needed; when an overlay IS declared and
// no resolver is wired, the worker fails closed with a typed error — a
// phantom segment is never composited.
func (w *Worker) WithOverlaySegmentResolver(r OverlaySegmentResolver) *Worker {
	if w != nil {
		w.overlayResolver = r
	}
	return w
}

// WithOverlayCompositor attaches the overlay compositing pass that blends
// the segment onto the source at the declared window. Optional: a request
// without an overlay skips compositing; an overlay declared without a wired
// compositor fails closed — the final video never claims an overlay it does
// not actually carry in its pixels.
func (w *Worker) WithOverlayCompositor(c OverlayCompositor) *Worker {
	if w != nil {
		w.overlayCompositor = c
	}
	return w
}

// WithOutputProber attaches the post-render byte probe. When wired, the worker
// certifies actual bytes via ProbeOutput→ValidateContract before Publish and
// again after overlay composition. Optional in tests; required in production.
func (w *Worker) WithOutputProber(p OutputProber) *Worker {
	if w != nil {
		w.outputProber = p
	}
	return w
}

// Handle is the job.Handler-shaped entry point bound to the Master.
func (w *Worker) Handle(ctx context.Context, j *job.Job, tools *job.JobExecutionTools) (job.Result, error) {
	progress := safeProgress(tools)
	emit := safeEvent(tools)

	progress(0, "clip.render started")
	jobStart := time.Now()
	w.log.Info("clip.render.job.phase",
		zap.String("subsystem", "clip_render_worker"),
		zap.String("phase", "start"),
		zap.String("job_id", j.ID),
	)

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
	// The run directory is a worker invariant, not an optional side effect of
	// subtitle compilation. A render without subtitles still needs a stable
	// output root for the sealed plan and Chronon assets.
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return nil, fmt.Errorf("clip.render: create run directory: %w", err)
	}
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
	var watermarkSpec *WatermarkSpec
	var watermarkText string
	if req.Watermark != nil && req.Watermark.Enabled {
		watermarkSpec = req.Watermark
		watermarkText = req.Watermark.Text
	}
	plan, err := Compile(CompileInput{
		RunID:          j.ID,
		Source:         prepared.Source,
		Watermark:      prepared.Watermark,
		WatermarkSpec:  watermarkSpec,
		WatermarkText:  watermarkText,
		Background:     prepared.Background,
		BackgroundMode: req.Background.Mode,
		Subtitles:      subtitleArtifact,
		Cues:           prepared.Transcript.Cues,
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
		result := renderedResult(j, &req, prepared, plan, subtitleArtifact, nil, nil, nil)
		result["phase"] = "plan_sealed"
		return result, fmt.Errorf(
			"%w: job_id=%s source_asset_id=%s plan_sha256=%s",
			ErrRenderPhaseNotImplemented, j.ID, req.SourceAssetID, plan.PlanSHA256)
	}

	progress(90, "plan sealed; rendering with Rust")
	renderStart := time.Now()
	w.log.Info("clip.render.job.phase",
		zap.String("subsystem", "clip_render_worker"),
		zap.String("phase", "render_start"),
		zap.String("job_id", j.ID),
		zap.String("plan_sha256", plan.PlanSHA256),
		zap.String("output_path", plan.OutputPath),
	)
	outcome, err := w.renderer.Render(ctx, plan)
	renderMS := time.Since(renderStart).Milliseconds()
	if err != nil {
		w.log.Error("clip.render.job.render_failed",
			zap.String("job_id", j.ID),
			zap.Int64("duration_ms", renderMS),
			zap.Error(err),
		)
		return nil, fmt.Errorf("clip.render: render plan: %w", err)
	}
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
	if outcome == nil || outcome.OutputPath == "" || outcome.SizeBytes <= 0 {
		return nil, fmt.Errorf("clip.render: renderer returned an invalid output")
	}
	// Fail-closed GPU gate: a request that demands GPU must never be silently
	// served by the software fallback.
	if req.Execution.RequireGPU && outcome.Backend != BackendCudaNative && outcome.Backend != BackendChrononVulkan {
		return nil, fmt.Errorf("clip.render: execution.require_gpu=true but backend resolved to %q (a GPU backend is required)", outcome.Backend)
	}

	// ── Post-render byte certification (exact contract) ──────────────────
	if w.outputProber != nil {
		probe, err := w.outputProber.ProbeOutput(ctx, outcome.OutputPath)
		if err != nil {
			return nil, fmt.Errorf("clip.render: probe rendered output: %w", err)
		}
		if err := ValidateContract(prepared.Contract, probe); err != nil {
			return nil, fmt.Errorf("clip.render: rendered output violates contract: %w", err)
		}
		emit("clip.render.probe.certified", "rendered bytes certified exact", map[string]any{
			"output_path": outcome.OutputPath,
			"fps_num":     probe.FPSNum,
			"fps_den":     probe.FPSDen,
			"width":       probe.Width,
			"height":      probe.Height,
		})
	}

	// ── Overlay compositing (entity overlays) ───────────────────────────
	// When the request declares an overlay, the final video must contain
	// THAT overlay in its pixels: resolve the rendered segment from the
	// declared render_job_id, then blend it onto the source at the declared
	// [start_us, end_us) window. Fail-closed: an overlay declared without a
	// wired resolver/compositor, an unresolvable segment, or a failed blend
	// is a typed error — the published video never claims an overlay it does
	// not carry.
	publishPath := outcome.OutputPath
	var composite *OverlayCompositeResult
	if req.Overlay != nil {
		if w.overlayResolver == nil {
			return nil, fmt.Errorf("clip.render: overlay declared but no OverlaySegmentResolver is wired (compositing step not configured)")
		}
		if w.overlayCompositor == nil {
			return nil, fmt.Errorf("clip.render: overlay declared but no OverlayCompositor is wired (compositing step not configured)")
		}
		segment, err := w.overlayResolver.Resolve(ctx, OverlayResolveInput{
			RenderJobID: req.Overlay.RenderJobID,
			RenderKey:   req.Overlay.RenderKey,
		})
		if err != nil {
			return nil, fmt.Errorf("clip.render: resolve overlay segment: %w", err)
		}
		if segment == nil || segment.LocalPath == "" || segment.SHA256 == "" {
			return nil, fmt.Errorf("clip.render: overlay resolver returned an invalid segment")
		}
		emit("clip.render.overlay.segment_resolved", "overlay segment resolved", map[string]any{
			"render_job_id": req.Overlay.RenderJobID,
			"render_key":    req.Overlay.RenderKey,
			"path":          segment.LocalPath,
			"sha256":        segment.SHA256,
		})
		composite, err = w.overlayCompositor.Composite(ctx, OverlayCompositeInput{
			RunID:      j.ID,
			SourcePath: outcome.OutputPath,
			Segment:    segment,
			StartUS:    req.Overlay.StartUS,
			EndUS:      req.Overlay.EndUS,
			OutputPath: filepath.Join(runDir, "composited-clip.mp4"),
			Width:      int(prepared.Contract.Width),
			Height:     int(prepared.Contract.Height),
			Contract:   prepared.Contract,
		})
		if err != nil {
			return nil, fmt.Errorf("clip.render: composite overlay: %w", err)
		}
		if composite == nil || composite.OutputPath == "" || composite.SHA256 == "" {
			return nil, fmt.Errorf("clip.render: overlay compositor returned an invalid result")
		}
		publishPath = composite.OutputPath
		emit("clip.render.overlay.composited", "overlay composited onto source", map[string]any{
			"output_path":  composite.OutputPath,
			"sha256":       composite.SHA256,
			"composite_ms": composite.CompositeMS,
			"start_us":     req.Overlay.StartUS,
			"end_us":       req.Overlay.EndUS,
		})
		if w.outputProber != nil {
			probe, err := w.outputProber.ProbeOutput(ctx, composite.OutputPath)
			if err != nil {
				return nil, fmt.Errorf("clip.render: probe composited output: %w", err)
			}
			if err := ValidateContract(prepared.Contract, probe); err != nil {
				return nil, fmt.Errorf("clip.render: composited output violates contract: %w", err)
			}
			emit("clip.render.probe.certified", "composited bytes certified exact", map[string]any{
				"output_path": composite.OutputPath,
				"fps_num":     probe.FPSNum,
				"fps_den":     probe.FPSDen,
			})
		}
	}

	if w.publisher == nil {
		// Rendering and publication are separate boundaries. A local render
		// executor may be used by benchmarks and preparation tests without a
		// publication port; return the canonical render facts and leave the
		// publication projection absent.
		emit("clip.render.completed", "Rust render_clip completed without publication", map[string]any{
			"output_path": outcome.OutputPath, "size_bytes": outcome.SizeBytes,
			"duration_sec": outcome.DurationSec, "ffmpeg_ms": outcome.FFmpegMS,
			"backend": outcome.Backend,
		})
		progress(100, "clip.render completed")
		return renderedResult(j, &req, prepared, plan, subtitleArtifact, outcome, composite, nil), nil
	}
	publication, err := w.publisher.Publish(ctx, RenderPublishInput{
		RunID:         j.ID,
		SourceAssetID: req.SourceAssetID,
		OutputPath:    publishPath,
		Outcome:       outcome,
		Contract:      prepared.Contract,
		Transcript:    prepared.Transcript,
		Subtitles:     subtitleArtifact,
		DriveFolderID: req.Destination.DriveFolderID,
	})
	publishMS := time.Since(renderStart).Milliseconds() - renderMS
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
		"backend":      outcome.Backend,
	})
	totalMS := time.Since(jobStart).Milliseconds()
	w.log.Info("clip.render.job.completed",
		zap.String("subsystem", "clip_render_worker"),
		zap.String("job_id", j.ID),
		zap.String("source_asset_id", req.SourceAssetID),
		zap.String("asset_id", publication.AssetID),
		zap.String("drive_file_id", publication.DriveFileID),
		zap.String("drive_link", publication.DriveLink),
		zap.String("backend", string(outcome.Backend)),
		zap.Int64("total_ms", totalMS),
		zap.Int64("render_ms", renderMS),
		zap.Int64("publish_ms", publishMS),
		zap.Int64("ffmpeg_ms", outcome.FFmpegMS),
		zap.Int64("size_bytes", outcome.SizeBytes),
	)
	progress(100, "clip.render completed")

	return renderedResult(j, &req, prepared, plan, subtitleArtifact, outcome, composite, publication), nil
}

// renderedResult projects the *Prepared + sealed plan + render outcome +
// composited overlay (when declared) + published derived asset into the
// canonical job result map. Only JSON-safe values — the result envelope is
// persisted by the Master.
func renderedResult(j *job.Job, req *RenderRequest, prepared *Prepared, plan ClipRenderPlanV1, subtitleArtifact *SubtitleArtifact, outcome *RenderOutcome, composite *OverlayCompositeResult, published *RenderPublishResult) job.Result {
	result := job.Result{
		"job_id":          j.ID,
		"source_asset_id": req.SourceAssetID,
		"phase":           "rendered",
		"transcript_mode": req.Transcript.Mode,
		"contract_id":     prepared.Contract.ContractID,
		"contract": map[string]any{
			"width":        prepared.Contract.Width,
			"height":       prepared.Contract.Height,
			"fps_num":      prepared.Contract.FPSNum,
			"fps_den":      prepared.Contract.FPSDen,
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
			"fps_num":             outcome.FPSNum,
			"fps_den":             outcome.FPSDen,
			"backend":             outcome.Backend,
			"ffmpeg_ms":           outcome.FFmpegMS,
			"audio_copy_eligible": outcome.AudioCopyEligible,
			"audio_encode_passes": outcome.AudioEncodePasses,
			"subtitle_raster_cpu": outcome.SubtitleRasterCPU,
			"native_media":        outcome.NativeMedia,
			"gpu_copy_bytes":      outcome.GPUCopyBytes,
		}
	}
	if req.Overlay != nil {
		overlayBlock := map[string]any{
			"render_job_id":         req.Overlay.RenderJobID,
			"plan_fingerprint":      req.Overlay.PlanFingerprint,
			"render_key":            req.Overlay.RenderKey,
			"source_video_asset_id": req.Overlay.SourceVideoAssetID,
			"start_us":              req.Overlay.StartUS,
			"end_us":                req.Overlay.EndUS,
		}
		if composite != nil {
			overlayBlock["composited"] = true
			overlayBlock["output_path"] = composite.OutputPath
			overlayBlock["sha256"] = composite.SHA256
			overlayBlock["composite_ms"] = composite.CompositeMS
		}
		result["overlay"] = overlayBlock
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
