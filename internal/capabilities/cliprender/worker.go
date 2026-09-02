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
	"math"
	"os"
	"path/filepath"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
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

// WithRenderExecutor attaches the RenderingGen/Chronon render boundary. A missing
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
	kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseSubmitted, jobStart, jobStart, kernobs.StageStatusCompleted, nil)
	kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseClaimed, jobStart, jobStart, kernobs.StageStatusCompleted, nil)
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
		zap.Bool("require_zero_copy", req.Execution.RequireZeroCopy),
	)
	progress(10, "request validated; running parallel preparation")

	// ── Observability: the clip.render serial chain is recorded on the
	// kernel RunReport bound to ctx (prepare → subtitles → render → probe →
	// overlay → publish). Each stage uses the worker's own measured anchors
	// (the same anchors feed metrics_v2), so the RunReport critical path is
	// the real wall chain — never accumulated work. No run bound to ctx →
	// RecordStage is a no-op (instrumentation never changes behaviour).
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

	// ── ASS artifact (subtitles enabled) ────────────────────────────────
	runDir := filepath.Join(w.workspaceDir, "runs", j.ID)
	// The run directory is a worker invariant, not an optional side effect of
	// subtitle compilation. A render without subtitles still needs a stable
	// output root for the sealed plan and Chronon assets.
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return nil, fmt.Errorf("clip.render: create run directory: %w", err)
	}
	var subtitleArtifact *SubtitleArtifact
	// Real instrumentation for subtitle_compile_ms: the canonical ASS compile
	// (deterministic bytes + validation) is measured here, where it actually
	// happens. When subtitles are disabled the phase stays NOT_INSTRUMENTED.
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
		DurationMS:     prepared.Source.DurationMS,
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
		result := renderedResult(j, &req, prepared, plan, subtitleArtifact, nil, nil, nil)
		result["phase"] = "plan_sealed"
		return result, fmt.Errorf(
			"%w: job_id=%s source_asset_id=%s plan_sha256=%s",
			ErrRenderPhaseNotImplemented, j.ID, req.SourceAssetID, plan.PlanSHA256)
	}

	progress(90, "plan sealed; rendering with Chronon")
	renderSlotStart := time.Now()
	renderSlotEnd := renderSlotStart
	kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseRenderSlot, renderSlotStart, renderSlotEnd, kernobs.StageStatusCompleted, nil)
	renderStart := time.Now()
	w.log.Info("clip.render.job.phase",
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
			o, rErr = w.renderer.Render(opCtx, plan)
			return rErr
		})
		return o, e
	}()
	renderEnd := time.Now()
	renderMS := renderEnd.Sub(renderStart).Milliseconds()
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
	// served by the software fallback. The GPU backends are Chronon (only
	// when certified) and the PATH B CUDA hybrid (only on hosts with the
	// NVDEC/NVENC chain and for plans it can render device-local).
	if outcome.Backend != BackendChrononVulkan {
		return nil, fmt.Errorf("clip.render: backend resolved to %q; only Chronon (%s) is permitted", outcome.Backend, BackendChrononVulkan)
	}
	if req.Execution.RequireZeroCopy && (outcome.VideoZeroCopy == nil || !*outcome.VideoZeroCopy) {
		return nil, fmt.Errorf("clip.render: execution.require_zero_copy=true but renderer did not certify video_zero_copy=true (backend=%q)", outcome.Backend)
	}
	// Fold the worker-measured phases into the adapter's V2 report (real
	// instrumentation only — a disabled phase stays NOT_INSTRUMENTED). The
	// job-level total is set at the end of the run, where the final wall time
	// is known, so unaccounted_ms spans preparation + selection + render +
	// publish exactly like the benchmark report.
	if outcome.Metrics == nil {
		outcome.Metrics = NewRenderMetricsV2()
	}
	// render_wall_ms: the worker's own wall around the render port call
	// (backend selection + execution). This is the honest render WALL the
	// benchmark needs to compare against the render WORK (the summed
	// startup/composite/encode phases): TotalMS is later overwritten with
	// the job-level total, so without this field the render wall would be
	// lost and the wall-vs-work distinction would be unanswerable.
	outcome.Metrics.RenderWallMS = Metric(renderMS)
	if subtitleCompileMS >= 0 {
		outcome.Metrics.SubtitleCompileMS = Metric(subtitleCompileMS)
	}
	// asset_materialize_ms: the preparer already tracks every materialize
	// phase (materialize_source/watermark/background) with real wall times;
	// fold their sum into the report so the benchmark can attribute the
	// "bring the assets to disk" cost (Drive downloads) instead of leaving it
	// in the unaccounted gap. No materialize phase recorded → stays
	// NOT_INSTRUMENTED.
	if assetMS := materializeWallMS(prepared.Timings); assetMS >= 0 {
		outcome.Metrics.AssetMaterializeMS = Metric(assetMS)
	}
	// The adapter normally derives frames from the outcome's media facts; a
	// boundary that returns a report without frames still gets the count
	// derived here from the same sealed facts (never a fake number).
	if outcome.Metrics.Frames == 0 && outcome.FPSNum > 0 && outcome.FPSDen > 0 {
		outcome.Metrics.Frames = int(math.Round(outcome.DurationSec * float64(outcome.FPSNum) / float64(outcome.FPSDen)))
	}

	// ── Canonical projection of the renderer-owned phase timings ────────
	// Chronon measured every render phase in
	// the V2 report; project them onto the Run as owner-measured operations
	// (typed projection, never a second timer) so the benchmark can answer
	// "where did the render seconds go" from the canonical run — the same
	// single source the report already owns. Phases that were not measured
	// stay absent: no fake zeros. This is the projection half of the
	// one-boundary-one-timer rule: the rust.render_clip operation above is
	// the render WALL (worker-owned), these operations are the render WORK
	// (engine-owned), and neither re-times the other's boundary.
	projectRendererPhases(ctx, outcome.Backend, outcome.Metrics)

	// ── Post-render byte certification (exact contract) ──────────────────
	if w.outputProber != nil {
		probeStart := time.Now()
		probe, err := w.outputProber.ProbeOutput(ctx, outcome.OutputPath)
		probeEnd := time.Now()
		kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: StageClipProbe}, probeStart, probeEnd, err)
		kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseHashProbe, probeStart, probeEnd, kernobs.StageStatusCompleted, err)
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
		compositeStart := time.Now()
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
		kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: StageClipOverlay}, compositeStart, time.Now(), err)
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
		// Post-composite byte certification is MANDATORY when an overlay was
		// composited. The compositor did a full decode+encode cycle — the
		// output MUST pass exact contract validation before it reaches
		// publication. Fail-closed: a missing prober when overlay is declared
		// is a configuration error.
		if w.outputProber == nil {
			return nil, fmt.Errorf("clip.render: overlay composited but no OutputProber is wired — composited bytes MUST be certified before publication")
		}
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

	if w.publisher == nil {
		// Rendering and publication are separate boundaries. A local render
		// executor may be used by benchmarks and preparation tests without a
		// publication port; return the canonical render facts and leave the
		// publication projection absent.
		emit("clip.render.completed", "Chronon render completed without publication", map[string]any{
			"output_path": outcome.OutputPath, "size_bytes": outcome.SizeBytes,
			"duration_sec": outcome.DurationSec, "ffmpeg_ms": outcome.FFmpegMS,
			"backend": outcome.Backend,
		})
		progress(100, "clip.render completed")
		finalizeMetrics(outcome.Metrics, time.Since(jobStart).Milliseconds(), outcome.DurationSec)
		return renderedResult(j, &req, prepared, plan, subtitleArtifact, outcome, composite, nil), nil
	}
	// The publish stage is the true publisher boundary (Drive upload + asset
	// commit), distinct from the render-side probe/overlay stages — so the
	// RunReport critical path separates the clip.render "drive" phase from
	// the render chain. Publication metrics come exclusively from the
	// publisher-owned report; no worker chronometer is copied into a V2 field.
	uploadSlotStart := time.Now()
	uploadSlotEnd := uploadSlotStart
	kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseUploadSlot, uploadSlotStart, uploadSlotEnd, kernobs.StageStatusCompleted, nil)
	publishStart := time.Now()
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
	publishEnd := time.Now()
	kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: StageClipPublish}, publishStart, publishEnd, err)
	kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseDrive, publishStart, publishEnd, kernobs.StageStatusCompleted, err)
	if err != nil {
		return nil, fmt.Errorf("clip.render: publish result: %w", err)
	}
	// Publication metrics have ONE chronometer owner: the publisher. When it
	// reports its measured walls they are projected into the canonical V2
	// report as-is — the worker never re-times publication with a second
	// chronometer:
	//   publication_total_ms = publisher total wall
	//   artifact_publish_ms  = hash + taxonomy + commit (local artifact work)
	//   drive_upload_ms      = max(video, sidecar upload) — the uploads run
	//                          concurrently, so the phase wall is the max,
	//                          never the sum.
	// The renderer finalize timing (Chronon publish_ms → renderer_finalize_ms)
	// was recorded by the Rust adapter and is never overwritten here. The
	// publisher owns publication_total_ms, artifact_publish_ms and
	// drive_upload_ms. If it does not provide a report, those fields remain
	// NOT_INSTRUMENTED rather than being populated with a second worker timer.
	var logPublishMS int64 = NotInstrumented
	if outcome.Metrics != nil {
		if pm := publication.Publish; pm != nil {
			outcome.Metrics.PublicationTotalMS = Metric(pm.TotalMS)
			outcome.Metrics.ArtifactPublishMS = Metric(pm.HashMS + pm.TaxonomyResolveMS + pm.AssetCommitMS)
			driveMS := pm.VideoUploadMS
			if pm.SidecarUploadMS > driveMS {
				driveMS = pm.SidecarUploadMS
			}
			outcome.Metrics.DriveUploadMS = Metric(driveMS)
			logPublishMS = pm.TotalMS
		}
	}
	if publication == nil || publication.AssetID == "" || publication.DriveFileID == "" {
		return nil, fmt.Errorf("clip.render: publisher returned an invalid publication")
	}
	emit("clip.render.completed", "Chronon render completed", map[string]any{
		"output_path":  outcome.OutputPath,
		"size_bytes":   outcome.SizeBytes,
		"duration_sec": outcome.DurationSec,
		"ffmpeg_ms":    outcome.FFmpegMS,
		"backend":      outcome.Backend,
	})
	totalMS := time.Since(jobStart).Milliseconds()
	finalizeMetrics(outcome.Metrics, totalMS, outcome.DurationSec)
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
		// Publication wall: the publisher-owned total when reported, else the
		// worker boundary wall (publishers without a metrics report).
		zap.Int64("renderer_finalize_ms", logPublishMS),
		zap.Int64("ffmpeg_ms", outcome.FFmpegMS),
		zap.Int64("size_bytes", outcome.SizeBytes),
	)
	progress(100, "clip.render completed")

	return renderedResult(j, &req, prepared, plan, subtitleArtifact, outcome, composite, publication), nil
}
