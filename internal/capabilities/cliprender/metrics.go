package cliprender

// metrics.go owns the canonical V2 execution report for a sealed
// ClipRenderPlanV1 render. One shape serves every backend (CUDA native, Chronon
// Vulkan, FFmpeg fallback); each component fills only the phases it actually
// measures, and everything else stays NOT_INSTRUMENTED.
//
// godlike/07 NO-FAKE-AVAILABILITY: a phase/counter that has no real
// instrumentation reports the string "NOT_INSTRUMENTED", NEVER 0. Zero means
// "we measured it and nothing happened"; the sentinel means "we do not know
// yet". The two are deliberately distinguishable on the wire and in logs.

import "encoding/json"

// NotInstrumented is the sentinel for a phase/counter with no real
// instrumentation yet. It serializes as the string "NOT_INSTRUMENTED".
const NotInstrumented int64 = -1

// Metric is an instrumented numeric value in the V2 report: a wall-clock
// phase duration in milliseconds, or a GPU byte/frame counter. The
// NotInstrumented sentinel (-1) marshals as the string "NOT_INSTRUMENTED";
// any other value marshals as a plain number.
type Metric int64

// MarshalJSON emits the sentinel as the string "NOT_INSTRUMENTED" so reports
// never fake a zero for an unmeasured phase.
func (m Metric) MarshalJSON() ([]byte, error) {
	if int64(m) == NotInstrumented {
		return []byte(`"NOT_INSTRUMENTED"`), nil
	}
	return json.Marshal(int64(m))
}

// RenderMetricsV2 is the canonical execution report. Field-by-field:
//
//   - Backend selection (measured by the executor adapter around the probe and
//     the resolver; NOT part of the accounted phase set — selection overhead
//     surfaces inside unaccounted_ms).
//   - Preparation phases (asset materialization + subtitle compilation happen
//     upstream of the executor seam, so they are NOT_INSTRUMENTED here until
//     the preparer reports them).
//   - Render phases. Backends that only report a coarse render duration (the
//     Rust boundary reports one ffmpeg_ms spanning decode→encode; the Chronon
//     adapter reports one chronon render invocation) map that duration onto
//     CompositeMS until per-phase metadata lands — the unaccounted_ms delta
//     then exactly exposes the "time outside the render" question.
//   - Throughput + GPU movement counters.
//   - Totals: TotalMS (adapter-measured, authoritative) and the derived
//     UnaccountedMS.
type RenderMetricsV2 struct {
	BackendProbeMS   Metric        `json:"backend_probe_ms"`
	BackendResolveMS Metric        `json:"backend_resolve_ms"`
	BackendSelected  RenderBackend `json:"backend_selected"`
	BackendAttempts  int           `json:"backend_attempts"`
	FallbackCount    int           `json:"fallback_count"`
	FallbackReason   string        `json:"fallback_reason,omitempty"`
	FailedBackendMS  Metric        `json:"failed_backend_ms"`

	AssetMaterializeMS Metric `json:"asset_materialize_ms"`
	SubtitleCompileMS  Metric `json:"subtitle_compile_ms"`

	RendererStartupMS Metric `json:"renderer_startup_ms"`
	DecodeMS          Metric `json:"decode_ms"`
	CompositeMS       Metric `json:"composite_ms"`
	SubtitleRasterMS  Metric `json:"subtitle_raster_ms"`
	WatermarkRasterMS Metric `json:"watermark_raster_ms"`
	FrameConversionMS Metric `json:"frame_conversion_ms"`
	EncodeMS          Metric `json:"encode_ms"`
	AudioMuxMS        Metric `json:"audio_mux_ms"`
	PublishMS         Metric `json:"publish_ms"`

	Frames         int     `json:"frames"`
	RenderFPS      float64 `json:"render_fps"`
	TotalFPS       float64 `json:"total_fps"`
	RealtimeFactor float64 `json:"realtime_factor"`

	GPUCopyBytes            Metric `json:"gpu_copy_bytes"`
	GPUReadbackBytes        Metric `json:"gpu_readback_bytes"`
	EncoderStagingCopyBytes Metric `json:"encoder_staging_copy_bytes"`
	NV12ToRGBAFrames        Metric `json:"nv12_to_rgba_frames"`
	RGBAToNV12Frames        Metric `json:"rgba_to_nv12_frames"`
	SubtitleRasterCPU       bool   `json:"subtitle_raster_cpu"`

	TotalMS       Metric `json:"total_ms"`
	UnaccountedMS Metric `json:"unaccounted_ms"`
}

// NewRenderMetricsV2 returns a report with every phase/counter marked
// NOT_INSTRUMENTED. Callers fill only what they actually measure; the adapter
// then computes the derived aggregates (unaccounted_ms, FPS, realtime factor).
func NewRenderMetricsV2() *RenderMetricsV2 {
	m := &RenderMetricsV2{}
	for _, p := range []*Metric{
		&m.BackendProbeMS, &m.BackendResolveMS, &m.FailedBackendMS,
		&m.AssetMaterializeMS, &m.SubtitleCompileMS,
		&m.RendererStartupMS, &m.DecodeMS, &m.CompositeMS, &m.SubtitleRasterMS,
		&m.WatermarkRasterMS, &m.FrameConversionMS, &m.EncodeMS, &m.AudioMuxMS,
		&m.PublishMS,
		&m.GPUCopyBytes, &m.GPUReadbackBytes, &m.EncoderStagingCopyBytes,
		&m.NV12ToRGBAFrames, &m.RGBAToNV12Frames,
		&m.TotalMS, &m.UnaccountedMS,
	} {
		*p = Metric(NotInstrumented)
	}
	return m
}

// Merge overlays an executor-provided partial report onto this report. Only
// phases the executor actually measured (non-sentinel) win, so the adapter's
// selection facts, total, and derived aggregates are never clobbered.
func (m *RenderMetricsV2) Merge(executor *RenderMetricsV2) {
	if executor == nil {
		return
	}
	merge := func(dst, src *Metric) {
		if int64(*src) != NotInstrumented {
			*dst = *src
		}
	}
	merge(&m.AssetMaterializeMS, &executor.AssetMaterializeMS)
	merge(&m.SubtitleCompileMS, &executor.SubtitleCompileMS)
	merge(&m.RendererStartupMS, &executor.RendererStartupMS)
	merge(&m.DecodeMS, &executor.DecodeMS)
	merge(&m.CompositeMS, &executor.CompositeMS)
	merge(&m.SubtitleRasterMS, &executor.SubtitleRasterMS)
	merge(&m.WatermarkRasterMS, &executor.WatermarkRasterMS)
	merge(&m.FrameConversionMS, &executor.FrameConversionMS)
	merge(&m.EncodeMS, &executor.EncodeMS)
	merge(&m.AudioMuxMS, &executor.AudioMuxMS)
	merge(&m.PublishMS, &executor.PublishMS)
	merge(&m.GPUCopyBytes, &executor.GPUCopyBytes)
	merge(&m.GPUReadbackBytes, &executor.GPUReadbackBytes)
	merge(&m.EncoderStagingCopyBytes, &executor.EncoderStagingCopyBytes)
	merge(&m.NV12ToRGBAFrames, &executor.NV12ToRGBAFrames)
	merge(&m.RGBAToNV12Frames, &executor.RGBAToNV12Frames)
}

// accountedPhases is the render-execution phase set UnaccountedMS is computed
// against. Backend selection (probe/resolve) is deliberately excluded — it is
// pipeline overhead that unaccounted_ms is meant to surface.
func (m *RenderMetricsV2) accountedPhases() []Metric {
	return []Metric{
		m.AssetMaterializeMS, m.SubtitleCompileMS, m.RendererStartupMS,
		m.DecodeMS, m.CompositeMS, m.SubtitleRasterMS, m.WatermarkRasterMS,
		m.FrameConversionMS, m.EncodeMS, m.AudioMuxMS, m.PublishMS,
	}
}

// Compute derives the aggregate metrics from the measured phases:
//
//   - UnaccountedMS = TotalMS − Σ(measured render phases). Non-instrumented
//     phases do not subtract, so the sentinel case (a coarse render mapped to
//     CompositeMS) surfaces exactly the "time outside the render" delta the
//     review benchmark asks about.
//   - TotalFPS / RealtimeFactor from TotalMS and the rendered duration.
//   - RenderFPS from CompositeMS only (the compositing phase), 0 when the
//     composite phase is not measured.
//
// It is idempotent and safe to call after every population step.
func (m *RenderMetricsV2) Compute(durationSec float64) {
	if int64(m.TotalMS) == NotInstrumented {
		m.UnaccountedMS = Metric(NotInstrumented)
	} else {
		var accounted int64
		for _, p := range m.accountedPhases() {
			if int64(p) != NotInstrumented {
				accounted += int64(p)
			}
		}
		total := int64(m.TotalMS)
		if total < accounted {
			total = accounted
		}
		m.UnaccountedMS = Metric(total - accounted)
	}

	if int64(m.TotalMS) != NotInstrumented && int64(m.TotalMS) > 0 && m.Frames > 0 {
		totalSec := float64(int64(m.TotalMS)) / 1000.0
		m.TotalFPS = float64(m.Frames) / totalSec
		if durationSec > 0 {
			m.RealtimeFactor = durationSec / totalSec
		}
	}
	if int64(m.CompositeMS) != NotInstrumented && int64(m.CompositeMS) > 0 && m.Frames > 0 {
		m.RenderFPS = float64(m.Frames) / (float64(int64(m.CompositeMS)) / 1000.0)
	}
}
