package renderinggen

import (
	"math"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
)

// full JSONB-backed telemetry document. Never replace it with a fresh empty
// report: doing so silently turns real engine measurements into zeroes in the
// localized_renders API payload.
func metricsFromChrononMetrics(n map[string]float64, frameCount int, durationUS int64) *cliprender.RenderMetricsV2 {
	m := cliprender.NewRenderMetricsV2()
	metric := func(keys ...string) (float64, bool) {
		for _, key := range keys {
			if v, ok := n[key]; ok && !math.IsNaN(v) && !math.IsInf(v, 0) {
				return v, true
			}
		}
		return 0, false
	}
	set := func(dst *cliprender.Metric, keys ...string) {
		if v, ok := metric(keys...); ok {
			*dst = cliprender.Metric(math.Round(v))
		}
	}
	setFloat := func(dst *float64, keys ...string) {
		if v, ok := metric(keys...); ok {
			*dst = v
		}
	}

	// Queue/worker walls and the Chronon exclusive timeline. RenderWallMS is
	// deliberately NOT set here: the clip.render worker owns that wall (the
	// render port call around submit + wait + materialize) and fills it after
	// Render returns. The engine's own exclusive render-loop wall rides the
	// nested RenderLoopMS diagnostic instead, so the two never conflate a
	// remote queue round-trip with the engine loop.
	set(&m.RendererStartupMS, "chronon_exclusive_wall_timeline_startup_ms")
	set(&m.ProbeMS, "chronon_exclusive_wall_timeline_ffprobe_ms")
	set(&m.DecodeMS, "chronon_job_gpu_video_decode_wall_ms", "chronon_job_gpu_decode_submit_ms")
	set(&m.CompositeMS, "chronon_job_gpu_cuda_composite_wall_us")
	if int64(m.CompositeMS) != cliprender.NotInstrumented {
		m.CompositeMS = cliprender.Metric(math.Round(float64(m.CompositeMS) / 1000))
	}
	set(&m.EncodeMS, "chronon_job_encoder_finalize_ms", "chronon_exclusive_wall_timeline_encoder_drain_finalize_ms")
	// Exclusive-wall decomposition nested inside the worker-owned render
	// wall: prepare / render_loop / mux + output finalize / validation. Each
	// is a measured engine phase; an absent key stays NOT_INSTRUMENTED.
	set(&m.PrepareMS, "chronon_exclusive_wall_timeline_prepare_ms", "chronon_job_prepare_ms")
	set(&m.RenderLoopMS, "chronon_exclusive_wall_timeline_render_loop_ms", "chronon_job_render_loop_wall_ms")
	set(&m.MuxFinalizeMS, "chronon_exclusive_wall_timeline_mux_finalize_ms", "chronon_job_mux_finalize_ms")
	set(&m.RendererOutputFinalizeMS, "chronon_exclusive_wall_timeline_output_finalize_ms", "chronon_job_output_finalize_ms")
	set(&m.ValidationMS, "chronon_exclusive_wall_timeline_validation_ms", "chronon_job_validation_ms")
	// Render-loop wait diagnostics: the engine's accumulated waits across the
	// loop, in milliseconds. The job block reports frame-slot and CUDA↔Vulkan
	// waits in microseconds; decode/encode waits are already milliseconds.
	if v, ok := metric("chronon_job_gpu_frame_slot_wait_us"); ok {
		m.FrameSlotWaitMS = cliprender.Metric(math.Round(v / 1000))
	}
	if v, ok := metric("chronon_job_gpu_cuda_vulkan_wait_submit_us"); ok {
		m.CUDAVulkanWaitMS = cliprender.Metric(math.Round(v / 1000))
	}
	set(&m.DecoderWaitMS, "chronon_job_gpu_decode_wait_ms")
	set(&m.EncoderBackpressureMS, "chronon_job_encoder_backpressure_wait_ms", "chronon_job_gpu_encode_wait_ms")

	// Receipt verification phases from Chronon's `<output>.receipt.json`
	// timing_ms. decode/count_frames run only under the normal/certify
	// policy; under the default fast policy they stay NOT_INSTRUMENTED while
	// probe/sha256/total carry the metadata-only receipt cost. The resolved
	// policy + aggregate status label the run (fast=1/normal=2/certify=3)
	// instead of inferring the policy from the presence of decode timings.
	set(&m.ReceiptSHA256MS, "chronon_receipt_sha256_ms")
	set(&m.ReceiptProbeMS, "chronon_receipt_probe_ms")
	set(&m.ReceiptCountFramesMS, "chronon_receipt_count_frames_ms")
	set(&m.ReceiptDecodeMS, "chronon_receipt_decode_ms")
	set(&m.ReceiptTotalMS, "chronon_receipt_total_ms")
	set(&m.VerificationPolicy, "chronon_receipt_verification_policy")
	set(&m.VerificationPassed, "chronon_receipt_verification_status")

	// GPU counters and resource profile.
	set(&m.GPUUploadBytes, "chronon_job_gpu_gpu_upload_bytes", "chronon_job_gpu_upload_bytes")
	set(&m.GPUReadbackBytes, "chronon_job_gpu_gpu_readback_bytes", "chronon_job_gpu_gpu_readback_bytes")
	set(&m.EncoderStagingCopyBytes, "chronon_job_gpu_encoder_staging_copy_bytes")
	set(&m.VRAMUsedPeakMB, "chronon_job_hardware_vram_used_peak_mb")
	set(&m.GPUUtilizationAvg, "chronon_job_hardware_gpu_utilization_avg")
	set(&m.GPUUtilizationPeak, "chronon_job_hardware_gpu_utilization_peak")
	set(&m.NVENCUtilizationAvg, "chronon_job_hardware_nvenc_utilization_avg")
	set(&m.NVDECUtilizationAvg, "chronon_job_hardware_nvdec_utilization_avg")
	set(&m.CUDACompositeFrames, "chronon_job_gpu_cuda_composite_frames")
	set(&m.NV12ToRGBAFrames, "chronon_job_gpu_nv12_to_rgba_frames")
	set(&m.RGBAToNV12Frames, "chronon_job_gpu_rgba_to_nv12_frames")

	// Artifact facts are authoritative for the output frame count. Summary
	// values are authoritative for engine throughput; they are not recomputed
	// from the worker wall, which would mix queue/download/publish time in.
	if frameCount > 0 {
		m.Frames = frameCount
	}
	// render_fps comes from Chronon's own summary (render_loop_fps counts
	// frames actually pushed through the loop). Recorded as engine-measured so
	// the worker's later Compute never overwrites it with a wall derivation
	// that mixes queue/download time into the engine throughput.
	if v, ok := metric("chronon_summary_render_loop_fps", "chronon_summary_render_only_fps"); ok {
		m.SetEngineRenderFPS(v)
	}
	setFloat(&m.TotalFPS, "chronon_summary_end_to_end_fps")
	setFloat(&m.RealtimeFactor, "chronon_summary_realtime_factor")
	if m.RealtimeFactor != 0 {
		m.SpeedFactor = m.RealtimeFactor
	}
	// ProcessingXRT is deliberately NOT derived here: RenderWallMS is owned
	// by the clip.render worker (filled after Render returns), so the XRT of
	// the render wall is computed in Compute once that wall exists.
	return m
}
