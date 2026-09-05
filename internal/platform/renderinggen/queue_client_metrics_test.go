package renderinggen

import (
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	queueclient "github.com/Marcuss-ops/RenderginGen/queue/client"
)

func TestMetricsFromChrononArtifactPropagatesEngineProfile(t *testing.T) {
	a := &queueclient.Artifact{
		FrameCount: 1176,
		DurationUS: 7_600_000,
		Metrics: map[string]float64{
			"chronon_summary_render_loop_fps":                177.633113,
			"chronon_summary_render_only_fps":                230.033376,
			"chronon_summary_end_to_end_fps":                 155.100584,
			"chronon_summary_realtime_factor":                6.462524,
			"chronon_exclusive_wall_timeline_render_loop_ms": 6620.387,
			"chronon_job_gpu_video_decode_wall_ms":           37,
			"chronon_job_gpu_cuda_composite_wall_us":         47833,
			"chronon_job_encoder_finalize_ms":                389.929,
			"chronon_job_hardware_vram_used_peak_mb":         10245,
			"chronon_job_gpu_gpu_readback_bytes":             0,
			"chronon_job_gpu_encoder_staging_copy_bytes":     0,
		},
	}
	m := metricsFromChrononMetrics(a.Metrics, a.FrameCount, a.DurationUS)
	if m.Frames != 1176 || m.RenderFPS != 177.633113 || m.TotalFPS != 155.100584 || m.RealtimeFactor != 6.462524 {
		t.Fatalf("summary not propagated: %+v", m)
	}
	if m.RenderWallMS != 6620 || m.DecodeMS != 37 || m.CompositeMS != 48 || m.EncodeMS != 390 {
		t.Fatalf("phase timings not propagated: render=%v decode=%v composite=%v encode=%v", m.RenderWallMS, m.DecodeMS, m.CompositeMS, m.EncodeMS)
	}
	if m.VRAMUsedPeakMB != 10245 || m.GPUReadbackBytes != 0 || m.EncoderStagingCopyBytes != 0 {
		t.Fatalf("GPU counters not propagated: vram=%v readback=%v staging=%v", m.VRAMUsedPeakMB, m.GPUReadbackBytes, m.EncoderStagingCopyBytes)
	}
	if m.RenderWallMS == cliprender.Metric(cliprender.NotInstrumented) {
		t.Fatal("render wall unexpectedly not instrumented")
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
