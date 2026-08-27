package cliprender

// worker_result.go contains the result projection helpers used by the
// clip.render worker. Extracted from worker.go to keep the main handler
// file under the 600-LOC strict gate.

import (
	"strings"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

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
		renderBlock := map[string]any{
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
			"gpu_copy_bytes":      outcome.GPUCopyBytes,
		}
		if outcome.Metrics != nil {
			renderBlock["metrics_v2"] = outcome.Metrics
		}
		result["render"] = renderBlock
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

// materializeWallMS sums the preparer's materialize phase walls
// (materialize_source/watermark/background) so asset_materialize_ms reflects
// the real "bring assets to disk" cost. Returns -1 when no materialize phase
// was recorded (the report then stays NOT_INSTRUMENTED).
func materializeWallMS(timings PreparationTimings) int64 {
	var total int64 = -1
	for _, phase := range timings.Phases {
		if strings.HasPrefix(phase.Phase, "materialize_") {
			if total < 0 {
				total = 0
			}
			total += phase.WallMS
		}
	}
	return total
}

// finalizeMetrics sets the job-level total wall time on the V2 report and
// recomputes the derived aggregates (unaccounted_ms, FPS, realtime factor).
// The job total is the authoritative total_ms of the report — it spans
// preparation + selection + render + publish, so unaccounted_ms surfaces the
// "time outside the render phases" question exactly like the benchmark asks.
func finalizeMetrics(m *RenderMetricsV2, jobTotalMS int64, durationSec float64) {
	if m == nil {
		return
	}
	m.TotalMS = Metric(jobTotalMS)
	m.Compute(durationSec)
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
