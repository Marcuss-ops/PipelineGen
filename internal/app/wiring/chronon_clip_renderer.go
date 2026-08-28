package wiring

// chronon_clip_renderer.go is the complex-render boundary. Rust continues to
// own acquisition/probing/mux/upload; Chronon owns the Vulkan text/composite
// graph. The adapter deliberately uses a plan + assets-root and never sends
// decoded video frames through Go memory.
//
// Every phase is logged with structured zap entries and per-phase timing is
// collected so the pipelinegen call chain (clip.render → chronon → ffprobe →
// audio mux → finalize) can be reconstructed from the server logs alone.

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	"go.uber.org/zap"
)

// chrononAwareCapabilityProbe decorates the canonical ffmpeg capability probe
// with the Chronon render binary's presence. Backend selection is the
// resolver's single authority, so the probe must honestly report whether the
// Chronon backend is configured at all — without this flag the resolver could
// never select chronon_vulkan (the adapter's try-and-fall-back shortcut that
// used to reach it is gone).
type chrononAwareCapabilityProbe struct {
	base       cliprender.BackendCapabilityProbe
	chrononBin string
}

func (p chrononAwareCapabilityProbe) ProbeCapabilities(ctx context.Context) (cliprender.RendererCapabilities, error) {
	caps, err := p.base.ProbeCapabilities(ctx)
	if err != nil {
		return caps, err
	}
	caps.ChrononVulkan = strings.TrimSpace(p.chrononBin) != ""
	return caps, nil
}

var _ cliprender.BackendCapabilityProbe = (*chrononAwareCapabilityProbe)(nil)

// watermarkPositionForSize resolves the world-space center position for a
// watermark of the GIVEN size. The ChrononPlanProjector passes the styled
// size (style.width_px/height_px overrides), so a resized logo keeps its
// declared position instead of being positioned by the original image size.
func watermarkPositionForSize(imgW, imgH int, position string, canvasW, canvasH, margin int) []int {
	if margin < 0 {
		margin = 0
	}
	if imgW <= 0 || imgH <= 0 {
		return []int{0, 0}
	}
	x, y := margin, margin
	switch position {
	case cliprender.PositionTopRight:
		x = canvasW - margin - imgW
	case cliprender.PositionBottomLeft:
		y = canvasH - margin - imgH
	case cliprender.PositionBottomRight:
		x = canvasW - margin - imgW
		y = canvasH - margin - imgH
	case cliprender.PositionCenter:
		x = (canvasW - imgW) / 2
		y = (canvasH - imgH) / 2
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	// Chronon image positions are world-space centers, not top-left pixels.
	return []int{x + imgW/2 - canvasW/2, y + imgH/2 - canvasH/2}
}

func watermarkDimensions(path string) (int, int) {
	imgW, imgH := 0, 0
	if f, err := os.Open(path); err == nil {
		if cfg, _, err := image.DecodeConfig(f); err == nil {
			imgW, imgH = cfg.Width, cfg.Height
		}
		_ = f.Close()
	}
	return imgW, imgH
}

type chrononClipRenderExecutor struct {
	binary    string
	ffmpeg    string
	log       *zap.Logger
	projector ChrononPlanProjector
}

// chrononPhaseMetrics records the wall-clock duration of every phase the
// Go side is responsible for. The result is logged once and returned so
// downstream handlers can surface it in the job envelope.
type chrononPhaseMetrics struct {
	SetupMS           int64 `json:"setup_ms"`
	ProbeDurationMS   int64 `json:"probe_duration_ms"`
	PlanSerializeMS   int64 `json:"plan_serialize_ms"`
	ChrononLockWaitMS int64 `json:"chronon_lock_wait_ms"`
	ChrononRenderMS   int64 `json:"chronon_render_ms"`
	AudioMuxMS        int64 `json:"audio_mux_ms"`
	TotalMS           int64 `json:"total_ms"`
}

func (m chrononPhaseMetrics) LogFields() []zap.Field {
	return []zap.Field{
		zap.Int64("setup_ms", m.SetupMS),
		zap.Int64("probe_duration_ms", m.ProbeDurationMS),
		zap.Int64("plan_serialize_ms", m.PlanSerializeMS),
		zap.Int64("chronon_lock_wait_ms", m.ChrononLockWaitMS),
		zap.Int64("chronon_render_ms", m.ChrononRenderMS),
		zap.Int64("audio_mux_ms", m.AudioMuxMS),
		zap.Int64("total_ms", m.TotalMS),
	}
}

// chrononTimingReport is the narrow projection of Chronon's canonical
// *.timing.json sidecar needed by RenderMetricsV2. Chronon remains the owner
// of these measurements; PipelineGen only transports them and never re-times
// or fabricates missing phases.
type chrononTimingReport struct {
	EncodeCloseMS float64 `json:"encode_close_ms"`
	FrameTimes    []struct {
		ConversionCopyMS float64 `json:"conversion_copy_ms"`
		EncoderMS        float64 `json:"encoder_ms"`
	} `json:"frame_times_ms"`
	Job struct {
		EngineInitMS      *float64 `json:"engine_init_ms"`
		BackendInitMS     *float64 `json:"backend_init_ms"`
		PlanReadMS        *float64 `json:"plan_read_ms"`
		PlanParseMS       *float64 `json:"plan_parse_ms"`
		PlanValidateMS    *float64 `json:"plan_validate_ms"`
		PlanCompileMS     *float64 `json:"plan_compile_ms"`
		GraphCompileMS    *float64 `json:"graph_compile_ms"`
		PrepareMS         *float64 `json:"prepare_ms"`
		OutputFinalizeMS  *float64 `json:"output_finalize_ms"`
		GPU               struct {
			CUDACompositeWallUS *uint64 `json:"cuda_composite_wall_us"`
			VideoDecodeWallMS    *uint64 `json:"video_decode_wall_ms"`
		} `json:"gpu"`
		Text struct {
			RasterMS      *float64 `json:"raster_ms"`
			AtlasUploadMS *float64 `json:"atlas_upload_ms"`
			DrawMS        *float64 `json:"draw_ms"`
		} `json:"text"`
		Image struct {
			ResolveMS *float64 `json:"resolve_ms"`
			DecodeMS  *float64 `json:"decode_ms"`
			ConvertMS *float64 `json:"convert_ms"`
			UploadMS  *float64 `json:"upload_ms"`
			DrawMS    *float64 `json:"draw_ms"`
		} `json:"image"`
		CPUBreakdown struct {
			CompositeNodeBlendMS *float64 `json:"compositenode_blend_ms"`
			EffectStackTotalMS   *float64 `json:"effect_stack_total_ms"`
		} `json:"cpu_breakdown"`
	} `json:"job"`
}

type chrononMeasuredPhases struct {
	StartupMS         *int64
	DecodeMS          *int64
	CompositeMS       *int64
	SubtitleRasterMS  *int64
	WatermarkRasterMS *int64
	FrameConversionMS *int64
	EncodeMS          *int64
	FinalizeMS        *int64
}

func sumMeasuredMS(values ...*float64) *int64 {
	var sum float64
	seen := false
	for _, value := range values {
		if value == nil {
			continue
		}
		seen = true
		sum += *value
	}
	if !seen {
		return nil
	}
	ms := int64(sum + 0.5)
	return &ms
}

func measuredUintMS(value *uint64) *int64 {
	if value == nil {
		return nil
	}
	ms := int64(*value)
	return &ms
}

func readChrononMeasuredPhases(path string, plan cliprender.ClipRenderPlanV1) (chrononMeasuredPhases, error) {
	var report chrononTimingReport
	raw, err := os.ReadFile(path)
	if err != nil {
		return chrononMeasuredPhases{}, err
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		return chrononMeasuredPhases{}, fmt.Errorf("decode chronon timing sidecar: %w", err)
	}

	phases := chrononMeasuredPhases{}
	phases.StartupMS = sumMeasuredMS(
		report.Job.EngineInitMS,
		report.Job.BackendInitMS,
		report.Job.PlanReadMS,
		report.Job.PlanParseMS,
		report.Job.PlanValidateMS,
		report.Job.PlanCompileMS,
		report.Job.GraphCompileMS,
		report.Job.PrepareMS,
	)
	phases.DecodeMS = measuredUintMS(report.Job.GPU.VideoDecodeWallMS)

	var compositeValues []*float64
	if report.Job.CPUBreakdown.CompositeNodeBlendMS != nil {
		compositeValues = append(compositeValues, report.Job.CPUBreakdown.CompositeNodeBlendMS)
	}
	if report.Job.CPUBreakdown.EffectStackTotalMS != nil {
		compositeValues = append(compositeValues, report.Job.CPUBreakdown.EffectStackTotalMS)
	}
	if report.Job.GPU.CUDACompositeWallUS != nil {
		cudaMS := float64(*report.Job.GPU.CUDACompositeWallUS) / 1000.0
		compositeValues = append(compositeValues, &cudaMS)
	}
	phases.CompositeMS = sumMeasuredMS(compositeValues...)

	textOverlayMS := sumMeasuredMS(
		report.Job.Text.RasterMS,
		report.Job.Text.AtlasUploadMS,
		report.Job.Text.DrawMS,
	)
	hasSubtitles := plan.Subtitles != nil && strings.TrimSpace(plan.Subtitles.Path) != ""
	hasTextWatermark := plan.Watermark != nil && strings.TrimSpace(plan.Watermark.Text) != ""
	hasImageWatermark := plan.Watermark != nil && strings.TrimSpace(plan.Watermark.Text) == "" && strings.TrimSpace(plan.Watermark.Path) != ""
	hasBackgroundAsset := plan.Background != nil && plan.Background.Mode == cliprender.BackgroundModeAsset && strings.TrimSpace(plan.Background.Path) != ""
	// Chronon's job.text aggregate covers all text nodes. Attribute it only
	// when exactly one text-overlay class is present; when subtitles and a
	// text watermark coexist we deliberately leave both fields unknown rather
	// than splitting one measured total by guesswork.
	if hasSubtitles && !hasTextWatermark {
		phases.SubtitleRasterMS = textOverlayMS
	}
	if hasTextWatermark && !hasSubtitles {
		phases.WatermarkRasterMS = textOverlayMS
	}
	// The job.image aggregate is watermark-specific only when no background
	// image is also present. In that safe case expose the complete measured
	// image watermark pipeline (resolve/decode/convert/upload/draw).
	if hasImageWatermark && !hasBackgroundAsset {
		phases.WatermarkRasterMS = sumMeasuredMS(
			report.Job.Image.ResolveMS,
			report.Job.Image.DecodeMS,
			report.Job.Image.ConvertMS,
			report.Job.Image.UploadMS,
			report.Job.Image.DrawMS,
		)
	}

	if len(report.FrameTimes) > 0 {
		var conversionMS float64
		var encodeMS float64
		for _, frame := range report.FrameTimes {
			conversionMS += frame.ConversionCopyMS
			encodeMS += frame.EncoderMS
		}
		conversion := int64(conversionMS + 0.5)
		encode := int64(encodeMS + report.EncodeCloseMS + 0.5)
		phases.FrameConversionMS = &conversion
		phases.EncodeMS = &encode
	}
	phases.FinalizeMS = sumMeasuredMS(report.Job.OutputFinalizeMS)
	return phases, nil
}

// Process-wide Chronon lifecycle is initialized once, but GPU work is admitted
// through a bounded semaphore (default C=2) instead of a global render mutex.
// The semaphore is context-aware and is shared by every Chronon render in this
// process; mux and publication happen after the permit is released.
var chrononRuntimeControlInit sync.Once

// NewChrononClipRenderExecutor constructs the adapter. log is mandatory: every
// render boundary decision and phase timing is emitted through it.
func NewChrononClipRenderExecutor(binary, ffmpeg string, log *zap.Logger) *chrononClipRenderExecutor {
	if strings.TrimSpace(binary) == "" {
		return nil
	}
	if log == nil {
		log = zap.NewNop()
	}
	// The projector is stateless; the zero value is the canonical projector.
	return &chrononClipRenderExecutor{binary: binary, ffmpeg: ffmpeg, log: log}
}

// logPhase emits a single chronon render phase line with consistent fields so
// the server log can be grep'd by run_id or phase to reconstruct timelines.
func (r *chrononClipRenderExecutor) logPhase(phase, runID string, fields ...zap.Field) {
	all := append([]zap.Field{
		zap.String("subsystem", "chronon_render"),
		zap.String("phase", phase),
		zap.String("run_id", runID),
	}, fields...)
	r.log.Info("clip.render.chronon.phase", all...)
}

func (r *chrononClipRenderExecutor) RenderClip(ctx context.Context, plan cliprender.ClipRenderPlanV1, _ cliprender.RenderBackend) (rustexec.ClipRenderResult, error) {
	if r == nil || strings.TrimSpace(r.binary) == "" {
		return rustexec.ClipRenderResult{}, fmt.Errorf("chronon renderer is not configured")
	}
	if err := plan.Validate(); err != nil {
		return rustexec.ClipRenderResult{}, err
	}
	started := time.Now()
	metrics := chrononPhaseMetrics{}

	r.logPhase("start", plan.RunID,
		zap.String("source_path", plan.Source.Path),
		zap.Int("output_width", plan.Output.Width),
		zap.Int("output_height", plan.Output.Height),
		zap.Int("fps_num", plan.Output.FPSNum),
		zap.Int("fps_den", plan.Output.FPSDen),
		zap.String("output_path", plan.OutputPath),
		zap.Bool("has_watermark", plan.Watermark != nil),
		zap.Bool("has_subtitles", plan.Subtitles != nil && strings.TrimSpace(plan.Subtitles.Path) != ""),
		zap.String("binary", r.binary),
	)

	// ── Phase 1: setup the chronon run directory and link assets ──────
	setupStart := time.Now()
	runRoot, err := os.MkdirTemp(filepath.Dir(plan.OutputPath), ".chronon-clip-*")
	if err != nil {
		return rustexec.ClipRenderResult{}, err
	}
	defer os.RemoveAll(runRoot)
	assets := filepath.Join(runRoot, "assets")
	if err := os.MkdirAll(filepath.Join(assets, "fonts"), 0o755); err != nil {
		return rustexec.ClipRenderResult{}, err
	}
	link := func(label, src, name string) error {
		if src == "" {
			return nil
		}
		dst := filepath.Join(assets, name)
		// Chronon validates the canonical path against assets-root, so a
		// symlink to an external materialized asset is intentionally rejected.
		// A hardlink keeps the operation zero-copy at the filesystem level;
		// only cross-filesystem materialization falls back to a byte copy.
		if err := os.Link(src, dst); err == nil {
			r.logPhase("asset_linked", plan.RunID,
				zap.String("label", label),
				zap.String("src", src),
				zap.String("dst", dst),
				zap.String("mode", "hardlink"),
			)
			return nil
		}
		r.logPhase("asset_link_fallback_copy", plan.RunID,
			zap.String("label", label),
			zap.String("src", src),
			zap.String("dst", dst),
		)
		in, openErr := os.Open(src)
		if openErr != nil {
			return openErr
		}
		defer in.Close()
		out, openErr := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if openErr != nil {
			return openErr
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
	if err := link("source_clip", plan.Source.Path, "clip.mp4"); err != nil {
		r.logPhase("setup_failed", plan.RunID, zap.String("stage", "asset_link"), zap.Error(err))
		return rustexec.ClipRenderResult{}, err
	}
	if plan.Watermark != nil && plan.Watermark.Text == "" && plan.Watermark.Path != "" {
		if err := link("watermark_image", plan.Watermark.Path, "watermark"+filepath.Ext(plan.Watermark.Path)); err != nil {
			r.logPhase("setup_failed", plan.RunID, zap.String("stage", "watermark_link"), zap.Error(err))
			return rustexec.ClipRenderResult{}, err
		}
	}
	if plan.Background != nil && plan.Background.Mode == cliprender.BackgroundModeAsset &&
		strings.TrimSpace(plan.Background.Path) != "" {
		if err := link("background", plan.Background.Path, "background"+filepath.Ext(plan.Background.Path)); err != nil {
			r.logPhase("setup_failed", plan.RunID, zap.String("stage", "background_link"), zap.Error(err))
			return rustexec.ClipRenderResult{}, err
		}
	}
	// The sealed ASS is the single canonical subtitle artifact — the same
	// bytes the Rust path burns via libass. Chronon consumes THIS file,
	// never a re-derived cue serialization (no SRT drift). The on-disk bytes
	// are re-audited against the plan hash before linking: a drifted artifact
	// fails closed exactly like the Rust re-audit.
	if plan.Subtitles != nil && strings.TrimSpace(plan.Subtitles.Path) != "" {
		gotSHA, _, hashErr := digest.SHA256File(plan.Subtitles.Path)
		if hashErr != nil {
			r.logPhase("setup_failed", plan.RunID, zap.String("stage", "subtitles_ass_hash"), zap.Error(hashErr))
			return rustexec.ClipRenderResult{}, fmt.Errorf("chronon: verify subtitle ASS %q: %w", plan.Subtitles.Path, hashErr)
		}
		if gotSHA != plan.Subtitles.SHA256 {
			r.logPhase("setup_failed", plan.RunID, zap.String("stage", "subtitles_ass_drift"),
				zap.String("got", gotSHA),
				zap.String("want", plan.Subtitles.SHA256),
			)
			return rustexec.ClipRenderResult{}, fmt.Errorf("chronon: subtitle ASS drift: got %q want %q", gotSHA, plan.Subtitles.SHA256)
		}
		if err := link("subtitles", plan.Subtitles.Path, "subtitles.ass"); err != nil {
			r.logPhase("setup_failed", plan.RunID, zap.String("stage", "subtitles_ass_link"), zap.Error(err))
			return rustexec.ClipRenderResult{}, err
		}
		r.logPhase("subtitles_ass_linked", plan.RunID,
			zap.Int("cue_count", len(plan.Subtitles.Cues)),
			zap.String("sha256", plan.Subtitles.SHA256),
		)
	}
	font := "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
	if _, err := os.Stat(font); err != nil {
		font = ""
	}
	if font != "" {
		_ = link("font", font, "fonts/DejaVuSans.ttf")
	}
	metrics.SetupMS = time.Since(setupStart).Milliseconds()
	r.logPhase("setup_done", plan.RunID,
		zap.String("run_root", runRoot),
		zap.String("assets_root", assets),
		zap.Int64("duration_ms", metrics.SetupMS),
	)

	// ── Phase 2: probe source duration via ffprobe ────────────────────
	probeStart := time.Now()
	durationMS := probeDurationMS(ctx, r.ffmpeg, plan.Source.Path)
	metrics.ProbeDurationMS = time.Since(probeStart).Milliseconds()
	if durationMS <= 0 && plan.Subtitles != nil {
		for _, c := range plan.Subtitles.Cues {
			if c.EndMs > durationMS {
				durationMS = c.EndMs
			}
		}
	}
	if durationMS <= 0 {
		r.logPhase("probe_failed", plan.RunID,
			zap.Int64("duration_ms", metrics.ProbeDurationMS),
			zap.String("ffmpeg", r.ffmpeg),
		)
		return rustexec.ClipRenderResult{}, fmt.Errorf("chronon: cannot determine source duration")
	}
	// ── Phase 3: project the sealed plan onto the Chronon wire shape ──
	// The ChrononPlanProjector is the SINGLE canonical projection: the layer
	// graph (video/background/watermark/subtitles), the style blocks and the
	// transition intents are lowered there — the executor never builds layer
	// maps itself. fps/frames derive from the probed duration inside the
	// projector (one owner of the canvas), and the typed plan is what gets
	// serialized.
	projectStart := time.Now()
	chrononPlan, projectErr := r.projector.Project(plan, durationMS)
	metrics.PlanSerializeMS = time.Since(projectStart).Milliseconds()
	if projectErr != nil {
		r.logPhase("plan_projection_failed", plan.RunID, zap.Error(projectErr))
		return rustexec.ClipRenderResult{}, projectErr
	}
	fpsNum, fpsDen := chrononPlan.Canvas.FPSNum, chrononPlan.Canvas.FPSDen
	fps := fpsNum / fpsDen
	frames := chrononPlan.Canvas.DurationFrames
	r.logPhase("probed", plan.RunID,
		zap.Int64("duration_ms", durationMS),
		zap.Int("fps", fps),
		zap.Int("frames", frames),
		zap.Int64("probe_ms", metrics.ProbeDurationMS),
	)

	// ── Phase 4: serialize + write the plan JSON ──────────────────────
	planSerializeStart := time.Now()
	planPath := filepath.Join(runRoot, "plan.json")
	b, _ := json.Marshal(chrononPlan)
	if err := os.WriteFile(planPath, b, 0o644); err != nil {
		r.logPhase("plan_write_failed", plan.RunID, zap.Error(err))
		return rustexec.ClipRenderResult{}, err
	}
	metrics.PlanSerializeMS += time.Since(planSerializeStart).Milliseconds()
	r.logPhase("plan_sealed", plan.RunID,
		zap.String("plan_path", planPath),
		zap.Int("layer_count", len(chrononPlan.Layers)),
		zap.Int("plan_size_bytes", len(b)),
		zap.Int64("duration_ms", metrics.PlanSerializeMS),
	)

	// ── Phase 4: invoke chronon render (the heavy phase) ──────────────
	// The video GPU build exposes the native FFmpeg/NVENC encoder. Keep the
	// pipe mode because it is the Chronon video export path, but use native
	// encoding so frames stay on the CUDA surface path instead of falling
	// back to a CPU libx264 subprocess.
	r.logPhase("chronon_render_start", plan.RunID,
		zap.String("binary", r.binary),
		zap.String("backend", "vulkan"),
		zap.String("hardware", "nvenc"),
		zap.String("ffmpeg_mode", "pipe"),
		zap.String("encoder_backend", "native"),
		zap.Int("expected_frames", frames),
		zap.Int("gpu_concurrency", currentChrononGPUConcurrency()),
	)
	chrononVideoPath := filepath.Join(runRoot, "chronon.mp4")
	cmd := exec.CommandContext(ctx, r.binary,
		"render",
		"--plan", planPath,
		"--output", chrononVideoPath,
		"--assets-root", assets,
		"--backend", "vulkan",
		"--hardware", "nvenc",
		"--ffmpeg-mode", "pipe",
		"--encoder-backend", "native",
		"--report",
	)

	metricsDir := filepath.Join(filepath.Dir(plan.OutputPath), "metrics")
	metricsDirReady := os.MkdirAll(metricsDir, 0o755) == nil
	chrononLogPath := filepath.Join(runRoot, "chronon.log")
	if metricsDirReady {
		chrononLogPath = filepath.Join(metricsDir, plan.RunID+".chronon.log")
	}
	wait, release, acquireErr := acquireChrononGPU(ctx)
	metrics.ChrononLockWaitMS = wait.Milliseconds()
	if acquireErr != nil {
		r.logPhase("chronon_render_admission_failed", plan.RunID,
			zap.Int64("wait_ms", metrics.ChrononLockWaitMS),
			zap.Error(acquireErr),
		)
		return rustexec.ClipRenderResult{}, fmt.Errorf("chronon GPU admission: %w", acquireErr)
	}
	r.logPhase("chronon_render_acquired_slot", plan.RunID,
		zap.Int64("wait_ms", metrics.ChrononLockWaitMS),
		zap.Int("gpu_concurrency", currentChrononGPUConcurrency()),
	)
	executionStart := time.Now()
	procOut, renderErr := runChrononCommandStreaming(cmd, chrononLogPath, plan.RunID, r.log)
	release()
	metrics.ChrononRenderMS = time.Since(executionStart).Milliseconds()
	out := procOut.Tail

	if renderErr != nil {
		previewLen := len(out)
		if previewLen > 4000 {
			previewLen = 4000
		}
		r.logPhase("chronon_render_failed", plan.RunID,
			zap.Int64("stdout_bytes", procOut.TotalBytes),
			zap.Int64("duration_ms", metrics.ChrononRenderMS),
			zap.String("stderr_tail", strings.TrimSpace(string(out[len(out)-previewLen:])),
			zap.Error(renderErr),
		)
		return rustexec.ClipRenderResult{}, fmt.Errorf("chronon render: %w: %s", renderErr, strings.TrimSpace(string(out)))
	}

	chrononSidecarPath := chrononVideoPath + ".timing.json"
	measured, timingErr := readChrononMeasuredPhases(chrononSidecarPath, plan)
	if timingErr != nil {
		r.logPhase("chronon_timing_unavailable", plan.RunID, zap.Error(timingErr))
	} else if metricsDirReady {
		if rawTiming, readErr := os.ReadFile(chrononSidecarPath); readErr == nil {
			if writeErr := os.WriteFile(filepath.Join(metricsDir, plan.RunID+".chronon.timing.json"), rawTiming, 0o644); writeErr != nil {
				r.logPhase("metrics_write_failed", plan.RunID, zap.String("artifact", "timing_sidecar"), zap.Error(writeErr))
			}
		}
	}
	r.logPhase("chronon_render_done", plan.RunID,
		zap.Int64("stdout_bytes", procOut.TotalBytes),
		zap.Int64("duration_ms", metrics.ChrononRenderMS),
		zap.String("output", chrononVideoPath),
	)

	// ── Phase 5: remux audio from the source onto the chronon video ──
	if err := os.MkdirAll(filepath.Dir(plan.OutputPath), 0o755); err != nil {
		return rustexec.ClipRenderResult{}, err
	}
	muxStart := time.Now()
	r.logPhase("audio_mux_start", plan.RunID,
		zap.String("video", chrononVideoPath),
		zap.String("source_audio", plan.Source.Path),
		zap.String("output", plan.OutputPath),
	)
	if err := muxChrononAudio(ctx, r.ffmpeg, chrononVideoPath, plan.Source.Path, plan.OutputPath); err != nil {
		metrics.AudioMuxMS = time.Since(muxStart).Milliseconds()
		r.logPhase("audio_mux_failed", plan.RunID,
			zap.Int64("duration_ms", metrics.AudioMuxMS),
			zap.Error(err),
		)
		return rustexec.ClipRenderResult{}, err
	}
	metrics.AudioMuxMS = time.Since(muxStart).Milliseconds()
	r.logPhase("audio_mux_done", plan.RunID,
		zap.Int64("duration_ms", metrics.AudioMuxMS),
	)

	st, err := os.Stat(plan.OutputPath)
	if err != nil || st.Size() == 0 {
		r.logPhase("finalize_failed", plan.RunID, zap.NamedError("stat_error", err))
		return rustexec.ClipRenderResult{}, fmt.Errorf("chronon output missing or empty")
	}

	metrics.TotalMS = time.Since(started).Milliseconds()
	r.log.Info("clip.render.chronon.completed", append([]zap.Field{
		zap.String("subsystem", "chronon_render"),
		zap.String("run_id", plan.RunID),
		zap.String("output_path", plan.OutputPath),
		zap.Int64("size_bytes", st.Size()),
		zap.Int64("duration_ms", durationMS),
	}, metrics.LogFields()...)...)

	probeMS := metrics.ProbeDurationMS
	opMS := metrics.TotalMS
	return rustexec.ClipRenderResult{
		OutputPath:        plan.OutputPath,
		SizeBytes:         st.Size(),
		DurationSec:       float64(durationMS) / 1000,
		Width:             uint32(plan.Output.Width),
		Height:            uint32(plan.Output.Height),
		FPSNum:            uint32(plan.Output.FPSNum),
		FPSDen:            uint32(plan.Output.FPSDen),
		FFmpegMS:          metrics.TotalMS,
		StartupMS:         measured.StartupMS,
		PublishMS:         measured.FinalizeMS,
		OpMS:              &opMS,
		ProbeMS:           &probeMS,
		DecodeMS:          measured.DecodeMS,
		FilterGraphMS:     measured.CompositeMS,
		SubtitleRasterMS:  measured.SubtitleRasterMS,
		WatermarkRasterMS: measured.WatermarkRasterMS,
		FrameConversionMS: measured.FrameConversionMS,
		EncodeMS:          measured.EncodeMS,
		AudioMuxMS:        &metrics.AudioMuxMS,
		AudioCopyEligible: boolPtr(true),
		AudioEncodePasses: intPtr(0),
		SubtitleRasterCPU: boolPtr(false),
	}, nil
}

func boolPtr(v bool) *bool { return &v }
func intPtr(v int) *int    { return &v }

func probeDurationMS(ctx context.Context, ffmpeg, path string) int64 {
	if cached, ok := chrononProbeLookup(path); ok {
		return cached
	}
	ffprobe := "ffprobe"
	if strings.TrimSpace(ffmpeg) != "" {
		ffprobe = filepath.Join(filepath.Dir(ffmpeg), "ffprobe")
	}
	out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", path).Output()
	if err != nil {
		return 0
	}
	sec, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0
	}
	durationMS := int64(sec*1000 + .5)
	chrononProbeStore(path, durationMS)
	return durationMS
}

func muxChrononAudio(ctx context.Context, ffmpeg, video, source, output string) error {
	bin := ffmpeg
	if strings.TrimSpace(bin) == "" {
		bin = "ffmpeg"
	}
	tmp := output + ".chronon-mux.tmp.mp4"
	_ = os.Remove(tmp)
	cmd := exec.CommandContext(ctx, bin, "-y", "-hide_banner", "-loglevel", "error", "-i", video, "-i", source, "-map", "0:v:0", "-map", "1:a?", "-c:v", "copy", "-c:a", "copy", "-shortest", tmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("chronon audio mux: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return os.Rename(tmp, output)
}
