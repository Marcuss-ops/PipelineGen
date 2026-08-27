package cliprender

// metrics_test.go — unit tests for the canonical V2 execution report
// (metrics.go): the NOT_INSTRUMENTED sentinel never fakes a zero, Merge only
// overlays measured phases, and Compute derives unaccounted_ms / FPS /
// realtime factor from the measured totals.

import (
	"encoding/json"
	"testing"
)

// TestMetricMarshal_SentinelAndValue verifies the wire contract: the
// NotInstrumented sentinel serializes as the string "NOT_INSTRUMENTED"
// (never a fake 0), while a real measurement serializes as a plain number.
func TestMetricMarshal_SentinelAndValue(t *testing.T) {
	raw, err := json.Marshal(Metric(NotInstrumented))
	if err != nil {
		t.Fatalf("marshal sentinel: %v", err)
	}
	if string(raw) != `"NOT_INSTRUMENTED"` {
		t.Fatalf("sentinel marshaled as %s, want \"NOT_INSTRUMENTED\"", raw)
	}
	raw, err = json.Marshal(Metric(42))
	if err != nil {
		t.Fatalf("marshal value: %v", err)
	}
	if string(raw) != `42` {
		t.Fatalf("value marshaled as %s, want 42", raw)
	}
}

// TestNewRenderMetricsV2_AllPhasesNotInstrumented verifies a fresh report
// marks every phase/counter NOT_INSTRUMENTED — zero must mean "measured and
// nothing happened", never "we did not look".
func TestNewRenderMetricsV2_AllPhasesNotInstrumented(t *testing.T) {
	m := NewRenderMetricsV2()
	phases := []Metric{
		m.BackendProbeMS, m.BackendResolveMS, m.FailedBackendMS,
		m.AssetMaterializeMS, m.SubtitleCompileMS, m.RendererStartupMS,
		m.DecodeMS, m.CompositeMS, m.SubtitleRasterMS, m.WatermarkRasterMS,
		m.FrameConversionMS, m.EncodeMS, m.AudioMuxMS, m.PublishMS,
		m.GPUCopyBytes, m.GPUReadbackBytes, m.EncoderStagingCopyBytes,
		m.NV12ToRGBAFrames, m.RGBAToNV12Frames, m.TotalMS, m.UnaccountedMS,
	}
	for _, p := range phases {
		if int64(p) != NotInstrumented {
			t.Errorf("phase = %d, want NotInstrumented for a fresh report", int64(p))
		}
	}
	// Scalar facts are zero-valued, not sentinel.
	if m.BackendSelected != "" || m.FallbackCount != 0 || m.BackendAttempts != 0 || m.Frames != 0 {
		t.Errorf("scalar defaults = %+v, want zero values", m)
	}
}

// TestRenderMetricsV2_MergeOnlyOverlaysMeasuredPhases verifies Merge lets an
// executor's real measurements win while leaving the adapter's selection
// facts and any sentinel phases untouched.
func TestRenderMetricsV2_MergeOnlyOverlaysMeasuredPhases(t *testing.T) {
	m := NewRenderMetricsV2()
	m.BackendSelected = BackendChrononVulkan
	m.BackendAttempts = 1
	m.CompositeMS = 5270
	m.EncodeMS = Metric(NotInstrumented) // executor did not measure encode

	executor := NewRenderMetricsV2()
	executor.EncodeMS = 550
	executor.GPUReadbackBytes = 0 // measured: genuinely zero readbacks

	m.Merge(executor)

	if m.EncodeMS != 550 {
		t.Fatalf("EncodeMS = %d, want 550 (measured phase overlays)", int64(m.EncodeMS))
	}
	if m.GPUReadbackBytes != 0 {
		t.Fatalf("GPUReadbackBytes = %d, want 0 (measured zero must survive Merge)", int64(m.GPUReadbackBytes))
	}
	if m.CompositeMS != 5270 {
		t.Fatalf("CompositeMS = %d, want 5270 (sentinel must not clobber)", int64(m.CompositeMS))
	}
	if m.BackendSelected != BackendChrononVulkan || m.BackendAttempts != 1 {
		t.Fatalf("selection facts clobbered: %+v", m)
	}
}

// TestRenderMetricsV2_ComputeUnaccounted verifies the headline number: with a
// coarse render mapped onto CompositeMS, unaccounted_ms = total_ms −
// composite_ms reproduces the review benchmark's "4.85s that are not
// explained by render + encode" exactly.
func TestRenderMetricsV2_ComputeUnaccounted(t *testing.T) {
	m := NewRenderMetricsV2()
	m.TotalMS = 10120 // 10.12s wall
	m.CompositeMS = 5270
	m.Frames = 240

	m.Compute(8.0)

	if m.UnaccountedMS != 4850 {
		t.Fatalf("unaccounted_ms = %d, want 4850 (10120 − 5270, the 4.85s gap)", int64(m.UnaccountedMS))
	}
	if m.RenderFPS < 45.4 || m.RenderFPS > 45.6 {
		t.Fatalf("render_fps = %.2f, want ~45.54 (240 frames / 5.27s composite)", m.RenderFPS)
	}
	if m.TotalFPS < 23.6 || m.TotalFPS > 23.8 {
		t.Fatalf("total_fps = %.2f, want ~23.7 (240 frames / 10.12s)", m.TotalFPS)
	}
	if m.RealtimeFactor < 0.79 || m.RealtimeFactor > 0.80 {
		t.Fatalf("realtime_factor = %.2f, want ~0.79 (8s / 10.12s)", m.RealtimeFactor)
	}
}

// TestRenderMetricsV2_ComputeSentinelTotal verifies that without a measured
// total the report does NOT fabricate an unaccounted_ms of zero — it stays
// NOT_INSTRUMENTED.
func TestRenderMetricsV2_ComputeSentinelTotal(t *testing.T) {
	m := NewRenderMetricsV2()
	m.CompositeMS = 100
	m.Compute(8.0)
	if int64(m.UnaccountedMS) != NotInstrumented {
		t.Fatalf("unaccounted_ms = %d, want NotInstrumented when total is unknown", int64(m.UnaccountedMS))
	}
}

// TestRenderMetricsV2_ComputeClampsOverrun verifies a total smaller than the
// accounted phases (clock skew between boundaries) never yields a negative
// unaccounted_ms.
func TestRenderMetricsV2_ComputeClampsOverrun(t *testing.T) {
	m := NewRenderMetricsV2()
	m.TotalMS = 100
	m.CompositeMS = 250
	m.Frames = 10
	m.Compute(8.0)
	if m.UnaccountedMS != 0 {
		t.Fatalf("unaccounted_ms = %d, want 0 (clamped — total cannot be negative)", int64(m.UnaccountedMS))
	}
}

// TestRenderMetricsV2_ReportSerializesSentinel verifies the full report
// marshals NOT_INSTRUMENTED phases as strings inside the JSON envelope, so
// logs and the job result never read as fake zeros.
func TestRenderMetricsV2_ReportSerializesSentinel(t *testing.T) {
	m := NewRenderMetricsV2()
	m.TotalMS = 500
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if decoded["decode_ms"] != "NOT_INSTRUMENTED" {
		t.Errorf("decode_ms = %v, want \"NOT_INSTRUMENTED\"", decoded["decode_ms"])
	}
	if decoded["total_ms"] != float64(500) {
		t.Errorf("total_ms = %v, want 500", decoded["total_ms"])
	}
	if _, ok := decoded["unaccounted_ms"]; !ok {
		t.Error("unaccounted_ms missing from serialized report")
	}
}

// TestRenderMetricsV2_WireShape pins the exact JSON contract of a populated
// report: measured phases serialize as numbers, unmeasured phases as the
// "NOT_INSTRUMENTED" string, selection facts under their review names, and
// fallback_reason absent when no fallback happened (omitempty). This is the
// contract the benchmark E2E consumes.
func TestRenderMetricsV2_WireShape(t *testing.T) {
	m := NewRenderMetricsV2()
	m.BackendSelected = BackendChrononVulkan
	m.BackendAttempts = 1
	m.BackendProbeMS = 12
	m.BackendResolveMS = 3
	m.CompositeMS = 5270
	m.Frames = 240
	m.SubtitleRasterCPU = true
	m.TotalMS = 10120
	m.Compute(8.0)

	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	t.Logf("wire shape:\n%s", raw)
	var d map[string]any
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	checks := map[string]any{
		"backend_selected":    "chronon_vulkan",
		"backend_attempts":    float64(1),
		"fallback_count":      float64(0),
		"backend_probe_ms":    float64(12),
		"backend_resolve_ms":  float64(3),
		"composite_ms":        float64(5270),
		"frames":              float64(240),
		"subtitle_raster_cpu": true,
		"total_ms":            float64(10120),
		"unaccounted_ms":      float64(4850),
	}
	for key, want := range checks {
		if d[key] != want {
			t.Errorf("%s = %v, want %v", key, d[key], want)
		}
	}
	// Unmeasured phases stay the sentinel string.
	for _, key := range []string{"decode_ms", "encode_ms", "audio_mux_ms",
		"asset_materialize_ms", "renderer_startup_ms", "gpu_readback_bytes",
		"nv12_to_rgba_frames", "encoder_staging_copy_bytes", "failed_backend_ms"} {
		if d[key] != "NOT_INSTRUMENTED" {
			t.Errorf("%s = %v, want \"NOT_INSTRUMENTED\"", key, d[key])
		}
	}
	// No fallback happened → fallback_reason is absent (omitempty), never an
	// empty string on the wire.
	if _, ok := d["fallback_reason"]; ok {
		t.Errorf("fallback_reason present without a fallback: %v", d["fallback_reason"])
	}

	// When a fallback IS recorded (a future certified-gate downgrade), the
	// field must serialize under its review name.
	m.FallbackCount = 1
	m.FallbackReason = "chronon_native_not_certified"
	raw, err = json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal with fallback: %v", err)
	}
	var d2 map[string]any
	if err := json.Unmarshal(raw, &d2); err != nil {
		t.Fatalf("unmarshal with fallback: %v", err)
	}
	if d2["fallback_reason"] != "chronon_native_not_certified" {
		t.Errorf("fallback_reason = %v, want the recorded reason", d2["fallback_reason"])
	}
}
