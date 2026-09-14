package renderinggen

import (
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	queueclient "github.com/Marcuss-ops/RenderingGen/queue/client"
)

func TestMetricsFromChrononArtifactPropagatesEngineProfile(t *testing.T) {
	a := &queueclient.Artifact{
		FrameCount: 1176,
		DurationUS: 7_600_000,
		Metrics: map[string]float64{
			"chronon_summary_render_loop_fps":                    177.633113,
			"chronon_summary_render_only_fps":                    230.033376,
			"chronon_summary_end_to_end_fps":                     155.100584,
			"chronon_summary_realtime_factor":                    6.462524,
			"chronon_exclusive_wall_timeline_render_loop_ms":     6620.387,
			"chronon_exclusive_wall_timeline_startup_ms":         565.0,
			"chronon_exclusive_wall_timeline_ffprobe_ms":         63.0,
			"chronon_exclusive_wall_timeline_prepare_ms":         1120.0,
			"chronon_exclusive_wall_timeline_mux_finalize_ms":    180.0,
			"chronon_exclusive_wall_timeline_output_finalize_ms": 20.0,
			"chronon_exclusive_wall_timeline_validation_ms":      160.0,
			"chronon_job_gpu_video_decode_wall_ms":               37,
			"chronon_job_gpu_cuda_composite_wall_us":             47833,
			"chronon_job_encoder_finalize_ms":                    389.929,
			"chronon_job_gpu_frame_slot_wait_us":                 1500000,
			"chronon_job_gpu_cuda_vulkan_wait_submit_us":         3700000,
			"chronon_job_gpu_decode_wait_ms":                     800,
			"chronon_job_encoder_backpressure_wait_ms":           1300.0,
			"chronon_job_hardware_vram_used_peak_mb":             10245,
			"chronon_job_gpu_gpu_readback_bytes":                 0,
			"chronon_job_gpu_encoder_staging_copy_bytes":         0,
		},
	}
	m := metricsFromChrononMetrics(a.Metrics, a.FrameCount, a.DurationUS)
	if m.Frames != 1176 || m.RenderFPS != 177.633113 || m.TotalFPS != 155.100584 || m.RealtimeFactor != 6.462524 {
		t.Fatalf("summary not propagated: %+v", m)
	}
	// Engine-measured fps must carry the engine authority flag so the worker's
	// later Compute never overwrites it with a wall derivation.
	if !m.EngineMeasuredRenderFPS() {
		t.Fatal("engine-measured render_fps must be recorded as authoritative")
	}
	if m.RenderLoopMS != 6620 || m.DecodeMS != 37 || m.CompositeMS != 48 || m.EncodeMS != 390 {
		t.Fatalf("phase timings not propagated: loop=%v decode=%v composite=%v encode=%v", m.RenderLoopMS, m.DecodeMS, m.CompositeMS, m.EncodeMS)
	}
	if m.RendererStartupMS != 565 || m.ProbeMS != 63 || m.PrepareMS != 1120 || m.MuxFinalizeMS != 180 || m.ValidationMS != 160 || m.RendererOutputFinalizeMS != 20 {
		t.Fatalf("exclusive-wall decomposition not propagated: startup=%v probe=%v prepare=%v mux=%v validation=%v finalize=%v",
			m.RendererStartupMS, m.ProbeMS, m.PrepareMS, m.MuxFinalizeMS, m.ValidationMS, m.RendererOutputFinalizeMS)
	}
	if m.FrameSlotWaitMS != 1500 || m.CUDAVulkanWaitMS != 3700 || m.DecoderWaitMS != 800 || m.EncoderBackpressureMS != 1300 {
		t.Fatalf("render-loop waits not propagated: frame_slot=%v cuda_vulkan=%v decoder=%v encoder_bp=%v",
			m.FrameSlotWaitMS, m.CUDAVulkanWaitMS, m.DecoderWaitMS, m.EncoderBackpressureMS)
	}
	if m.VRAMUsedPeakMB != 10245 || m.GPUReadbackBytes != 0 || m.EncoderStagingCopyBytes != 0 {
		t.Fatalf("GPU counters not propagated: vram=%v readback=%v staging=%v", m.VRAMUsedPeakMB, m.GPUReadbackBytes, m.EncoderStagingCopyBytes)
	}
	// RenderWallMS is the worker-owned port wall and is never set by the
	// adapter (the worker fills it after Render returns).
	if m.RenderWallMS != cliprender.Metric(cliprender.NotInstrumented) {
		t.Fatalf("render wall = %v, want NOT_INSTRUMENTED from the adapter (worker-owned)", int64(m.RenderWallMS))
	}
}

func TestMetricsFromChrononArtifactLeavesNestedDiagnosticsUninstrumented(t *testing.T) {
	m := metricsFromChrononMetrics(map[string]float64{
		"chronon_exclusive_wall_timeline_render_loop_ms": 6620.387,
	}, 1, 0)
	if m.RenderLoopMS != 6620 {
		t.Fatalf("render loop = %v, want 6620", m.RenderLoopMS)
	}
	// Phases the sidecar did not measure stay NOT_INSTRUMENTED — never a fake
	// zero.
	for name, phase := range map[string]cliprender.Metric{
		"prepare_ms":              m.PrepareMS,
		"mux_finalize_ms":         m.MuxFinalizeMS,
		"validation_ms":           m.ValidationMS,
		"renderer_finalize_ms":    m.RendererOutputFinalizeMS,
		"frame_slot_wait_ms":      m.FrameSlotWaitMS,
		"cuda_vulkan_wait_ms":     m.CUDAVulkanWaitMS,
		"decoder_wait_ms":         m.DecoderWaitMS,
		"encoder_backpressure_ms": m.EncoderBackpressureMS,
	} {
		if int64(phase) != cliprender.NotInstrumented {
			t.Errorf("%s = %v, want NOT_INSTRUMENTED", name, int64(phase))
		}
	}
}

func TestMetricsFromChrononArtifactKeepsUnknownFieldsUninstrumented(t *testing.T) {
	m := metricsFromChrononMetrics(nil, 1, 0)
	if m.EncodeMS != cliprender.Metric(cliprender.NotInstrumented) {
		t.Fatalf("missing encode timing became %v", m.EncodeMS)
	}
	if m.RenderFPS != 0 || m.TotalFPS != 0 || m.RealtimeFactor != 0 {
		t.Fatalf("missing summary became fabricated values: %+v", m)
	}
}

// TestMetricsFromChrononArtifactProjectsRendererMeasuredPhases pins the
// projection of the phases RenderingGen measures and the adapter used to drop:
// the renderer's own output probe and output finalize, the Chronon process
// service wall, and the worker's prep->GPU lane rendezvous wait. Those are the
// buckets that explain a 36.7 s worker wall around an 8.2 s engine render.
func TestMetricsFromChrononArtifactProjectsRendererMeasuredPhases(t *testing.T) {
	m := metricsFromChrononMetrics(map[string]float64{
		"probe_ms":                1120.99,
		"publish_ms":              160.533,
		"chronon_job_job_wall_ms": 8201.392,
		"gpu_lane_wait_ms":        27000.0,
	}, 1176, 0)
	if m.ProbeMS != 1121 {
		t.Errorf("probe_ms = %v, want 1121 (renderer output probe)", int64(m.ProbeMS))
	}
	if m.RendererOutputFinalizeMS != 161 {
		t.Errorf("renderer_finalize_ms = %v, want 161 (renderer output finalize)", int64(m.RendererOutputFinalizeMS))
	}
	if m.ChrononServiceMS != 8201 {
		t.Errorf("chronon_service_ms = %v, want 8201 (Chronon job wall)", int64(m.ChrononServiceMS))
	}
	if m.ChrononQueueWaitMS != 27000 {
		t.Errorf("chronon_queue_wait_ms = %v, want 27000 (GPU lane rendezvous)", int64(m.ChrononQueueWaitMS))
	}
}

// TestMetricsFromChrononArtifactDoesNotDoubleCountRendererInternalPhases pins
// the deliberate NON-mapping: RenderingGen's asset_materialize_ms and
// subtitle_burn_ms are phases of the renderer, i.e. they already live inside
// the worker-owned render wall. Projecting them onto AssetMaterializeMS /
// SubtitleCompileMS would count them twice, because those two fields are
// accounted as PipelineGen's own upstream work.
func TestMetricsFromChrononArtifactDoesNotDoubleCountRendererInternalPhases(t *testing.T) {
	m := metricsFromChrononMetrics(map[string]float64{
		"asset_materialize_ms":  0.989,
		"subtitle_burn_ms":      0.286,
		"overlay_compile_ms":    0.259,
		"plan_ms":               0.206,
		"sha256_ms":             0.14,
		"objectstore_upload_ms": 82.606,
	}, 1, 0)
	if m.AssetMaterializeMS != cliprender.Metric(cliprender.NotInstrumented) {
		t.Errorf("asset_materialize_ms = %v, want NOT_INSTRUMENTED (renderer-internal phase, carried from the resume instead)", int64(m.AssetMaterializeMS))
	}
	if m.SubtitleCompileMS != cliprender.Metric(cliprender.NotInstrumented) {
		t.Errorf("subtitle_compile_ms = %v, want NOT_INSTRUMENTED (renderer-internal phase)", int64(m.SubtitleCompileMS))
	}
}
