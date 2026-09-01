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
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	"go.uber.org/zap"
)

type chrononClipRenderExecutor struct {
	binary    string
	socket    string
	ffmpeg    string
	log       *zap.Logger
	projector ChrononPlanProjector
	// chrononMetrics is the optional Chronon Metrics Adapter. When wired, the
	// fine-grained sidecar phases (startup/input_open/prepare/render_loop/
	// encoder_drain/ffprobe/sha256) are projected into the durable
	// performance registry (performance_operations) after every render —
	// best-effort, never failing the render. Optional: benchmarks and unit
	// tests may omit it.
	chrononMetrics *cliprender.ChrononMetricsAdapter
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

// Process-wide Chronon lifecycle is initialized once, but GPU work is admitted
// through a bounded semaphore (default C=2) instead of a global render mutex.
// The semaphore is context-aware and is shared by every Chronon render in this
// process; mux and publication happen after the permit is released.
var chrononRuntimeControlInit sync.Once

// NewChrononClipRenderExecutor constructs the adapter. log is mandatory: every
// render boundary decision and phase timing is emitted through it.
func NewChrononClipRenderExecutor(binary, ffmpeg string, log *zap.Logger) *chrononClipRenderExecutor {
	socket := strings.TrimSpace(os.Getenv("CHRONON_RENDER_SOCKET"))
	if socket == "" {
		// RenderingGen's warm-worker entrypoint publishes the same daemon
		// through CHRONON_SOCKET_PATH. Accept it here as the canonical
		// deployment spelling while retaining the PipelineGen-specific alias.
		socket = strings.TrimSpace(os.Getenv("CHRONON_SOCKET_PATH"))
	}
	if strings.TrimSpace(binary) == "" && socket == "" {
		return nil
	}
	if log == nil {
		log = zap.NewNop()
	}
	// The projector is stateless; the zero value is the canonical projector.
	return &chrononClipRenderExecutor{binary: binary, socket: socket, ffmpeg: ffmpeg, log: log}
}

// WithChrononMetrics attaches the Chronon Metrics Adapter. It is optional and
// nil-tolerant: without it the render simply skips the performance-registry
// projection.
func (r *chrononClipRenderExecutor) WithChrononMetrics(a *cliprender.ChrononMetricsAdapter) *chrononClipRenderExecutor {
	if r != nil {
		r.chrononMetrics = a
	}
	return r
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
	if r == nil || (strings.TrimSpace(r.binary) == "" && strings.TrimSpace(r.socket) == "") {
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
	if plan.Watermark != nil {
		if strings.TrimSpace(plan.Watermark.Text) != "" && plan.Watermark.Path == "" {
			wmPNG := filepath.Join(runRoot, "watermark_text.png")
			fontSize := 48
			if plan.Watermark.Style != nil && plan.Watermark.Style.FontSizePX > 0 {
				fontSize = int(plan.Watermark.Style.FontSizePX)
			}
			escapedText := strings.ReplaceAll(plan.Watermark.Text, "'", "\\'")
			filter := fmt.Sprintf("drawtext=text='%s':fontcolor=white:fontsize=%d:x=(w-text_w)/2:y=(h-text_h)/2", escapedText, fontSize)
			cmd := exec.Command(r.ffmpeg,
				"-y",
				"-f", "lavfi",
				"-i", "color=c=black@0.0:s=400x120:d=0.1",
				"-vf", filter,
				"-vframes", "1",
				wmPNG,
			)
			if err := cmd.Run(); err == nil {
				plan.Watermark.Path = wmPNG
				plan.Watermark.Text = ""
			}
		}
		if plan.Watermark.Path != "" {
			if err := link("watermark_image", plan.Watermark.Path, "watermark"+filepath.Ext(plan.Watermark.Path)); err != nil {
				r.logPhase("setup_failed", plan.RunID, zap.String("stage", "watermark_link"), zap.Error(err))
				return rustexec.ClipRenderResult{}, err
			}
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
	// A video-only plan is the certification probe for Chronon's zero-copy
	// Direct-YUV path.  The backend name alone is not sufficient: Vulkan also
	// hosts the slower FullGraph path.  Carry the explicit execution request
	// through both the CLI and daemon protocols so a regression cannot silently
	// turn this probe into a FullGraph benchmark.
	directYUV := (plan.Watermark == nil || plan.Watermark.Path != "") &&
		(plan.Subtitles == nil || plan.Subtitles.Mode != "burn" || len(plan.Subtitles.Cues) == 0) &&
		(plan.Background == nil || plan.Background.Mode == cliprender.BackgroundModeNone)
	gpuHotPathMode := "auto"
	if directYUV {
		gpuHotPathMode = "require_direct_yuv"
	}
	r.logPhase("execution_request", plan.RunID,
		zap.String("gpu_hot_path_mode", gpuHotPathMode),
		zap.Bool("direct_yuv_probe", directYUV),
	)
	chrononVideoPath := filepath.Join(runRoot, "chronon.mp4")
	metricsDir := filepath.Join(filepath.Dir(plan.OutputPath), "metrics")
	if err := os.MkdirAll(metricsDir, 0o755); err != nil {
		r.logPhase("setup_failed", plan.RunID,
			zap.String("stage", "metrics_directory"),
			zap.String("path", metricsDir),
			zap.Error(err),
		)
		return rustexec.ClipRenderResult{}, fmt.Errorf("chronon: prepare metrics directory: %w", err)
	}
	metricsDirReady := true
	chrononLogPath := filepath.Join(runRoot, "chronon.log")
	if metricsDirReady {
		safeID := strings.ReplaceAll(plan.RunID, "/", "_")
		chrononLogPath = filepath.Join(metricsDir, safeID+".chronon.log")
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
	var procOut chrononProcessOutput
	var renderErr error
	if r.socket != "" {
		payload, marshalErr := json.Marshal(map[string]any{
			"plan_path":         planPath,
			"output":            chrononVideoPath,
			"assets_root":       assets,
			"backend":           "vulkan",
			"hardware_encoder":  "nvenc",
			"encoder_backend":   "native",
			"ffmpeg_mode":       "pipe",
			"report":            true,
			"gpu_hot_path_mode": gpuHotPathMode,
		})
		if marshalErr != nil {
			renderErr = marshalErr
		} else {
			procOut, renderErr = runChrononIPC(ctx, r.socket, payload, chrononLogPath, plan.RunID, r.log)
		}
	} else {
		cmd := exec.CommandContext(ctx, r.binary,
			"render",
			"--plan", planPath,
			"--output", chrononVideoPath,
			"--assets-root", assets,
			"--backend", "vulkan",
			"--hardware", "nvenc",
			"--ffmpeg-mode", "pipe",
			"--encoder-backend", "native",
			"--gpu-hot-path-mode", gpuHotPathMode,
			"--preset", "fast",
			"--report",
		)
		procOut, renderErr = runChrononCommandStreaming(cmd, chrononLogPath, plan.RunID, r.log)
	}
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
			zap.String("stderr_tail", strings.TrimSpace(string(out[len(out)-previewLen:]))),
			zap.Error(renderErr),
		)
		return rustexec.ClipRenderResult{}, fmt.Errorf("chronon render: %w: %s", renderErr, strings.TrimSpace(string(out)))
	}

	// Chronon owns these measurements. Read the sidecar once, before the
	// temporary run directory is cleaned, and project both its measured
	// phases and its typed GPU counters. The sidecar JSON stays a
	// transport/debug payload; the durable copy in metrics/ and the
	// performance-registry projection (when the Chronon Metrics Adapter is
	// wired) become the canonical history. A sidecar read/parse failure is
	// best-effort only for non-certified fallback renders. Direct-YUV renders
	// fail closed: without the engine-owned measurement there is no valid
	// certification claim. The certified output facts come from the sealed
	// plan + the chronon.mp4 bytes (the exact artifact the phases measured).
	chrononSidecarPath := chrononVideoPath + ".timing.json"
	var rawTiming []byte
	var measured chrononMeasuredPhases
	var chrononProjection rustexec.ClipRenderResult
	var timingErr error
	if rawTiming, timingErr = os.ReadFile(chrononSidecarPath); timingErr != nil {
		r.logPhase("chronon_timing_unavailable", plan.RunID, zap.Error(timingErr))
		if directYUV {
			return rustexec.ClipRenderResult{}, fmt.Errorf("chronon: direct-yuv certification failed: timing sidecar unavailable: %w", timingErr)
		}
	} else {
		if metricsDirReady {
			if writeErr := os.WriteFile(filepath.Join(metricsDir, plan.RunID+".chronon.timing.json"), rawTiming, 0o644); writeErr != nil {
				r.logPhase("metrics_write_failed", plan.RunID, zap.String("artifact", "timing_sidecar"), zap.Error(writeErr))
			}
		}
		measured, timingErr = readChrononMeasuredPhases(chrononSidecarPath, plan)
		chrononProjection, _ = readChrononProjection(chrononSidecarPath)
		if timingErr != nil {
			r.logPhase("chronon_timing_unavailable", plan.RunID, zap.Error(timingErr))
			if directYUV {
				return rustexec.ClipRenderResult{}, fmt.Errorf("chronon: direct-yuv certification failed: invalid timing sidecar: %w", timingErr)
			}
		} else {
			if directYUV && measured.ExecutionPath != "direct_yuv" {
				r.logPhase("chronon_direct_yuv_gate_failed", plan.RunID,
					zap.String("execution_path", measured.ExecutionPath),
					zap.String("required", "direct_yuv"),
				)
				return rustexec.ClipRenderResult{}, fmt.Errorf("chronon: direct-yuv certification failed: execution_path=%q", measured.ExecutionPath)
			}
			r.logPhase("chronon_timing_projected", plan.RunID,
				zap.String("sidecar", chrononSidecarPath),
				zap.Bool("gpu_readback_present", chrononProjection.GPUReadbackBytes != nil),
				zap.Bool("gpu_upload_present", chrononProjection.GPUUploadBytes != nil),
			)
		}
		// ── Chronon Metrics Adapter ───────────────────────────────────
		// The fine-grained sidecar phases are the engine's own exclusive-wall
		// measurements (startup/input_open/prepare/render_loop/encoder_drain/
		// ffprobe/sha256). When the adapter is wired they are promoted to the
		// canonical durable performance registry (performance_operations)
		// through the OperationReportProjectionRecorder seam. A parse/record
		// failure is logged, never a render failure.
		if r.chrononMetrics != nil {
			doc, parseErr := cliprender.ParseChrononSidecar(rawTiming)
			if parseErr != nil {
				r.logPhase("chronon_metrics_parse_failed", plan.RunID, zap.Error(parseErr))
			} else {
				opts := cliprender.ChrononMetricsPublishOptions{
					SourceSHA256:     plan.Source.SHA256,
					SourceDurationMS: durationMS,
					Width:            plan.Output.Width,
					Height:           plan.Output.Height,
					FPS:              float64(fps),
				}
				if chrononOut, statErr := os.Stat(chrononVideoPath); statErr == nil {
					opts.OutputSizeBytes = chrononOut.Size()
				}
				r.chrononMetrics.Publish(ctx, doc, opts)
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
	result := rustexec.ClipRenderResult{
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
	}
	if timingErr == nil {
		result.GPUReadbackBytes = chrononProjection.GPUReadbackBytes
		result.GPUUploadBytes = chrononProjection.GPUUploadBytes
		result.EncoderStagingCopyBytes = chrononProjection.EncoderStagingCopyBytes
		result.NV12ToRGBAFrames = chrononProjection.NV12ToRGBAFrames
		result.RGBAToNV12Frames = chrononProjection.RGBAToNV12Frames
		result.CUDACompositeFrames = chrononProjection.CUDACompositeFrames
		result.DecodeMS = chrononProjection.DecodeMS
		result.FilterGraphMS = chrononProjection.FilterGraphMS
		result.GPUUtilizationAvg = chrononProjection.GPUUtilizationAvg
		result.GPUUtilizationPeak = chrononProjection.GPUUtilizationPeak
		result.NVENCUtilizationAvg = chrononProjection.NVENCUtilizationAvg
		result.NVDECUtilizationAvg = chrononProjection.NVDECUtilizationAvg
		result.VRAMUsedPeakMB = chrononProjection.VRAMUsedPeakMB
		// Keep a durable copy for the benchmark/operator output.
		metricsDir := filepath.Join(filepath.Dir(plan.OutputPath), "metrics")
		if err := os.MkdirAll(metricsDir, 0o755); err == nil {
			if data, err := os.ReadFile(chrononSidecarPath); err == nil {
				_ = os.WriteFile(filepath.Join(metricsDir, plan.RunID+".chronon.timing.json"), data, 0o644)
			}
		}
	}
	return result, nil
}
