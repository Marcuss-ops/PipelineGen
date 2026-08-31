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
//
// SSOT RULE: this struct is the ONLY canonical metrics contract for clip
// render. Every backend (CUDA native, Chronon Vulkan, FFmpeg fallback)
// projects into it; the preparer and the publisher fold the chronometers
// they own into it; the Rust boundary's legacy scalars (ffmpeg_ms,
// subtitle_raster_cpu, gpu_copy_bytes) and the job result's render.* legacy
// keys are read-only compatibility projections of the same measured values.
// No component may compute a second, independent value for a field this
// report owns, and no new instrumentation may land in a parallel contract.

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

// RenderMetricsV2 is the canonical execution report. Diagnostic sub-phases
// may be nested inside top-level walls. Compute therefore reconciles TotalMS
// with exclusive top-level walls first and only falls back to fine-grained
// render work when a render wall is unavailable. This prevents subtitle,
// watermark, conversion and encoder diagnostics from being counted twice.
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
	// ProbeMS is the ffprobe source-probe wall reported separately from
	// RendererStartupMS (which excludes it), so the report can attribute the
	// probe instead of burying it inside startup.
	ProbeMS Metric `json:"probe_ms"`
	// ChrononQueueWaitMS is time waiting for PipelineGen's bounded GPU
	// admission permit. ChrononServiceMS is only the Chronon process service
	// wall while that permit is held. Both are diagnostics nested inside the
	// worker-owned RenderWallMS and are never added on top of it.
	ChrononQueueWaitMS Metric `json:"chronon_queue_wait_ms"`
	ChrononServiceMS   Metric `json:"chronon_service_ms"`
	DecodeMS           Metric `json:"decode_ms"`
	CompositeMS        Metric `json:"composite_ms"`
	SubtitleRasterMS   Metric `json:"subtitle_raster_ms"`
	WatermarkRasterMS  Metric `json:"watermark_raster_ms"`
	FrameConversionMS  Metric `json:"frame_conversion_ms"`
	EncodeMS           Metric `json:"encode_ms"`
	AudioMuxMS         Metric `json:"audio_mux_ms"`

	// Publication is split by ownership. RendererOutputFinalizeMS is the
	// renderer-side output finalize. PublicationTotalMS is the publisher wall;
	// ArtifactPublishMS and DriveUploadMS are diagnostics nested inside it.
	RendererOutputFinalizeMS Metric `json:"renderer_finalize_ms"`
	ArtifactPublishMS        Metric `json:"artifact_publish_ms"`
	DriveUploadMS            Metric `json:"drive_upload_ms"`
	PublicationTotalMS       Metric `json:"publication_total_ms"`
	PublishMS                Metric `json:"publish_ms,omitempty"`

	// RenderWallMS is the worker-owned top-level wall around backend selection
	// + execution. Fine-grained renderer timings are diagnostics inside this
	// wall and must not be added to it during reconciliation.
	RenderWallMS Metric `json:"render_wall_ms"`

	Frames    int     `json:"frames"`
	RenderFPS float64 `json:"render_fps"`
	TotalFPS  float64 `json:"total_fps"`
	// RealtimeFactor is retained as the compatibility field for the speed
	// factor. It is the inverse of ProcessingXRT: media duration / total wall.
	RealtimeFactor float64 `json:"realtime_factor"`
	SpeedFactor    float64 `json:"speed_factor"`
	ProcessingXRT  float64 `json:"processing_xrt"`

	GPUCopyBytes            Metric `json:"gpu_copy_bytes"`
	GPUUploadBytes          Metric `json:"gpu_upload_bytes"`
	GPUReadbackBytes        Metric `json:"gpu_readback_bytes"`
	PeakRSSBytes            Metric `json:"peak_rss_bytes"`
	DiskReadBytes           Metric `json:"disk_read_bytes"`
	DiskWriteBytes          Metric `json:"disk_write_bytes"`
	NetworkRXBytes          Metric `json:"network_rx_bytes"`
	NetworkTXBytes          Metric `json:"network_tx_bytes"`
	EncoderStagingCopyBytes Metric `json:"encoder_staging_copy_bytes"`
	NV12ToRGBAFrames        Metric `json:"nv12_to_rgba_frames"`
	RGBAToNV12Frames        Metric `json:"rgba_to_nv12_frames"`
	CUDACompositeFrames     Metric `json:"cuda_composite_frames"`
	GPUUtilizationAvg       Metric `json:"gpu_utilization_avg"`
	GPUUtilizationPeak      Metric `json:"gpu_utilization_peak"`
	NVENCUtilizationAvg     Metric `json:"nvenc_utilization_avg"`
	NVDECUtilizationAvg     Metric `json:"nvdec_utilization_avg"`
	VRAMUsedPeakMB          Metric `json:"vram_used_peak_mb"`
	SubtitleRasterCPU       bool   `json:"subtitle_raster_cpu"`
	// VideoZeroCopy is nil when the executor did not certify the strict
	// device-local path. It is a pointer intentionally: false is a measured
	// software/readback path, nil is not instrumented.
	VideoZeroCopy *bool `json:"video_zero_copy,omitempty"`

	TotalMS Metric `json:"total_ms"`
	// UnaccountedMS is retained for wire compatibility. UnattributedMS is the
	// explicit name used by new consumers. They are always identical.
	UnaccountedMS  Metric `json:"unaccounted_ms"`
	UnattributedMS Metric `json:"unattributed_ms"`
}

// NewRenderMetricsV2 returns a report with every phase/counter marked
// NOT_INSTRUMENTED. Callers fill only what they actually measure.
func NewRenderMetricsV2() *RenderMetricsV2 {
	m := &RenderMetricsV2{}
	for _, p := range []*Metric{
		&m.BackendProbeMS, &m.BackendResolveMS, &m.FailedBackendMS,
		&m.AssetMaterializeMS, &m.SubtitleCompileMS,
		&m.RendererStartupMS, &m.ProbeMS, &m.ChrononQueueWaitMS, &m.ChrononServiceMS,
		&m.DecodeMS, &m.CompositeMS, &m.SubtitleRasterMS, &m.WatermarkRasterMS,
		&m.FrameConversionMS, &m.EncodeMS, &m.AudioMuxMS,
		&m.RendererOutputFinalizeMS, &m.ArtifactPublishMS, &m.DriveUploadMS,
		&m.PublicationTotalMS, &m.PublishMS, &m.RenderWallMS,
		&m.GPUCopyBytes, &m.GPUReadbackBytes, &m.PeakRSSBytes, &m.DiskReadBytes,
		&m.GPUUploadBytes,
		&m.DiskWriteBytes, &m.NetworkRXBytes, &m.NetworkTXBytes, &m.EncoderStagingCopyBytes,
		&m.NV12ToRGBAFrames, &m.RGBAToNV12Frames, &m.CUDACompositeFrames,
		&m.GPUUtilizationAvg, &m.GPUUtilizationPeak, &m.NVENCUtilizationAvg,
		&m.NVDECUtilizationAvg, &m.VRAMUsedPeakMB,
		&m.TotalMS, &m.UnaccountedMS, &m.UnattributedMS,
	} {
		*p = Metric(NotInstrumented)
	}
	return m
}

// Merge overlays an executor-provided partial report onto this report. Only
// phases the executor actually measured (non-sentinel) win.
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
	merge(&m.ProbeMS, &executor.ProbeMS)
	merge(&m.ChrononQueueWaitMS, &executor.ChrononQueueWaitMS)
	merge(&m.ChrononServiceMS, &executor.ChrononServiceMS)
	merge(&m.DecodeMS, &executor.DecodeMS)
	merge(&m.CompositeMS, &executor.CompositeMS)
	merge(&m.SubtitleRasterMS, &executor.SubtitleRasterMS)
	merge(&m.WatermarkRasterMS, &executor.WatermarkRasterMS)
	merge(&m.FrameConversionMS, &executor.FrameConversionMS)
	merge(&m.EncodeMS, &executor.EncodeMS)
	merge(&m.AudioMuxMS, &executor.AudioMuxMS)
	merge(&m.RendererOutputFinalizeMS, &executor.RendererOutputFinalizeMS)
	merge(&m.ArtifactPublishMS, &executor.ArtifactPublishMS)
	merge(&m.DriveUploadMS, &executor.DriveUploadMS)
	merge(&m.PublicationTotalMS, &executor.PublicationTotalMS)
	merge(&m.PublishMS, &executor.PublishMS)
	merge(&m.RenderWallMS, &executor.RenderWallMS)
	merge(&m.GPUCopyBytes, &executor.GPUCopyBytes)
	merge(&m.GPUUploadBytes, &executor.GPUUploadBytes)
	merge(&m.GPUReadbackBytes, &executor.GPUReadbackBytes)
	merge(&m.PeakRSSBytes, &executor.PeakRSSBytes)
	merge(&m.DiskReadBytes, &executor.DiskReadBytes)
	merge(&m.DiskWriteBytes, &executor.DiskWriteBytes)
	merge(&m.NetworkRXBytes, &executor.NetworkRXBytes)
	merge(&m.NetworkTXBytes, &executor.NetworkTXBytes)
	merge(&m.EncoderStagingCopyBytes, &executor.EncoderStagingCopyBytes)
	merge(&m.NV12ToRGBAFrames, &executor.NV12ToRGBAFrames)
	merge(&m.RGBAToNV12Frames, &executor.RGBAToNV12Frames)
	merge(&m.CUDACompositeFrames, &executor.CUDACompositeFrames)
	merge(&m.GPUUtilizationAvg, &executor.GPUUtilizationAvg)
	merge(&m.GPUUtilizationPeak, &executor.GPUUtilizationPeak)
	merge(&m.NVENCUtilizationAvg, &executor.NVENCUtilizationAvg)
	merge(&m.NVDECUtilizationAvg, &executor.NVDECUtilizationAvg)
	merge(&m.VRAMUsedPeakMB, &executor.VRAMUsedPeakMB)
}

func measured(m Metric) (int64, bool) {
	if int64(m) == NotInstrumented {
		return 0, false
	}
	return int64(m), true
}

// exclusiveAccountedMS returns non-overlapping wall time. Diagnostic subphase
// metrics are deliberately excluded whenever their owning wall is available.
func (m *RenderMetricsV2) exclusiveAccountedMS() int64 {
	var total int64
	add := func(metric Metric) bool {
		if v, ok := measured(metric); ok {
			total += v
			return true
		}
		return false
	}

	// Upstream work whose only available measurement is its owned subphase.
	add(m.AssetMaterializeMS)
	add(m.SubtitleCompileMS)

	// Render wall owns queue + executor service + mux and every engine
	// diagnostic. If the worker wall is unavailable, fall back to a set of
	// non-overlapping engine-owned phases. Subtitle/watermark timings are
	// nested diagnostics of CompositeMS and are never added separately.
	if !add(m.RenderWallMS) {
		add(m.RendererStartupMS)
		add(m.ProbeMS)
		add(m.ChrononQueueWaitMS)
		if _, serviceMeasured := measured(m.ChrononServiceMS); serviceMeasured {
			add(m.ChrononServiceMS)
		} else {
			add(m.DecodeMS)
			add(m.CompositeMS)
			add(m.FrameConversionMS)
			add(m.EncodeMS)
			add(m.RendererOutputFinalizeMS)
		}
		add(m.AudioMuxMS)
	}

	// PublicationTotalMS owns hash + concurrent Drive uploads + taxonomy +
	// durable asset commit. Only fall back to child diagnostics if the owner
	// wall is absent; drive and artifact work can overlap, so max is safer
	// than an invalid sum in that fallback case.
	if !add(m.PublicationTotalMS) {
		artifact, artifactOK := measured(m.ArtifactPublishMS)
		drive, driveOK := measured(m.DriveUploadMS)
		switch {
		case artifactOK && driveOK:
			if artifact > drive {
				total += artifact
			} else {
				total += drive
			}
		case artifactOK:
			total += artifact
		case driveOK:
			total += drive
		}
	}
	return total
}

// Compute derives aggregate metrics from real owner-measured walls.
func (m *RenderMetricsV2) Compute(durationSec float64) {
	if int64(m.TotalMS) == NotInstrumented {
		m.UnaccountedMS = Metric(NotInstrumented)
		m.UnattributedMS = Metric(NotInstrumented)
	} else {
		accounted := m.exclusiveAccountedMS()
		total := int64(m.TotalMS)
		unattributed := total - accounted
		if unattributed < 0 {
			// Clock granularity/rounding may make a set of owner walls exceed the
			// enclosing wall by a few milliseconds. Negative mystery time is not
			// meaningful, so clamp the derived diagnostic only.
			unattributed = 0
		}
		m.UnaccountedMS = Metric(unattributed)
		m.UnattributedMS = Metric(unattributed)
	}

	if int64(m.TotalMS) != NotInstrumented && int64(m.TotalMS) > 0 && m.Frames > 0 {
		totalSec := float64(int64(m.TotalMS)) / 1000.0
		m.TotalFPS = float64(m.Frames) / totalSec
		if durationSec > 0 {
			m.SpeedFactor = durationSec / totalSec
			m.RealtimeFactor = m.SpeedFactor
		}
	}
	if int64(m.RenderWallMS) != NotInstrumented && int64(m.RenderWallMS) >= 0 && durationSec > 0 {
		renderSec := float64(int64(m.RenderWallMS)) / 1000.0
		m.ProcessingXRT = renderSec / durationSec
		if renderSec > 0 {
			m.SpeedFactor = durationSec / renderSec
		}
	}
	if int64(m.CompositeMS) != NotInstrumented && int64(m.CompositeMS) > 0 && m.Frames > 0 {
		m.RenderFPS = float64(m.Frames) / (float64(int64(m.CompositeMS)) / 1000.0)
	}
}
