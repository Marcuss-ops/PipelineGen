package cliprender

// worker_result.go contains the result projection helpers used by the
// clip.render worker. Extracted from worker.go to keep the main handler
// file under the 600-LOC strict gate.

import (
	"context"
	"strings"
	"sync"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// renderedResult projects the *Prepared + sealed plan + render outcome +
// composited overlay (when declared) + published derived asset into the
// canonical job result map. Only JSON-safe values — the result envelope is
// persisted by the Master.
func renderedResult(j *job.Job, req *RenderRequest, prepared *Prepared, plan ClipRenderPlanV1, subtitleArtifact *SubtitleArtifact, outcome *RenderOutcome, composite *OverlayCompositeResult, published *RenderPublishResult) job.Result {
	jobID := ""
	if j != nil {
		jobID = j.ID
	}
	if req == nil {
		req = &RenderRequest{Transcript: &TranscriptSpec{}}
	} else if req.Transcript == nil {
		req.Transcript = &TranscriptSpec{}
	}
	if prepared == nil {
		prepared = &Prepared{Contract: &ResolvedContract{}, Transcript: &TranscriptResult{}}
	}
	if prepared.Contract == nil {
		prepared.Contract = &ResolvedContract{}
	}
	if prepared.Transcript == nil {
		prepared.Transcript = &TranscriptResult{}
	}
	if prepared.Source == nil {
		prepared.Source = &MaterializedAsset{}
	}
	if prepared.Watermark == nil {
		// materializationResult is nil-safe; no initialization needed.
	}
	backgroundMode := ""
	if plan.Background != nil {
		backgroundMode = plan.Background.Mode
	}
	result := job.Result{
		"job_id":          jobID,
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
			"background":  backgroundMode,
		},
		"source": materializationResult(prepared.Source),
		"transcript": map[string]any{
			"language":            prepared.Transcript.Language,
			"reused":              prepared.Transcript.Reused,
			"text_sha256":         prepared.Transcript.TextSHA256,
			"cues":                len(prepared.Transcript.Cues),
			"source_audio_sha256": prepared.Transcript.SourceAudioSHA256,
		},
		"materialization": map[string]any{
			"source":     materializationResult(prepared.Source),
			"watermark":  materializationResult(prepared.Watermark),
			"background": materializationResult(prepared.Background),
		},
		"timings": map[string]any{
			"total_wall_ms": prepared.Timings.TotalWallMS,
			"total_work_ms": prepared.Timings.TotalWorkMS,
			"parallel":      prepared.Timings.Parallel,
			"phases":        prepared.Timings.Phases,
		},
	}
	if prepared.Watermark != nil {
		result["watermark"] = materializationResult(prepared.Watermark)
	}
	if prepared.Background != nil {
		result["background"] = materializationResult(prepared.Background)
	}
	if subtitleArtifact != nil {
		cacheFacts := subtitleCacheFactsFor(subtitleArtifact.LocalPath)
		subtitles := map[string]any{
			"path":               subtitleArtifact.LocalPath,
			"sha256":             subtitleArtifact.SHA256,
			"mode":               subtitleArtifact.Mode,
			"content_cache_hit":  cacheFacts.ContentCacheHit,
			"artifact_cache_hit": cacheFacts.ArtifactCacheHit,
		}
		if !cacheFacts.Measured {
			delete(subtitles, "content_cache_hit")
			delete(subtitles, "artifact_cache_hit")
		}
		result["subtitles"] = subtitles
	}
	if outcome != nil {
		renderBlock := map[string]any{
			"output_path":  outcome.OutputPath,
			"size_bytes":   outcome.SizeBytes,
			"duration_sec": outcome.DurationSec,
			"width":        outcome.Width,
			"height":       outcome.Height,
			"fps_num":      outcome.FPSNum,
			"fps_den":      outcome.FPSDen,
			"backend":      outcome.Backend,
			// Read-only compatibility projection of the canonical MetricsV2
			// report — same measured values, never a second computation.
			// ffmpeg_ms has no dedicated V2 field: it is the raw Rust boundary
			// scalar (the FFmpeg-fallback backend maps it onto
			// metrics_v2.composite_ms).
			"ffmpeg_ms":           outcome.FFmpegMS,
			"audio_copy_eligible": outcome.AudioCopyEligible,
			"audio_encode_passes": outcome.AudioEncodePasses,
			"subtitle_raster_cpu": outcome.SubtitleRasterCPU,
			"gpu_copy_bytes":      outcome.GPUCopyBytes,
			"video_zero_copy":     outcome.VideoZeroCopy,
		}

		if outcome.Metrics != nil {
			renderBlock["render_wall_ms"] = outcome.Metrics.RenderWallMS
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

// SubtitleCacheFacts records cache ownership at both levels: generated ASS
// content and the materialized run-local artifact. Availability is explicit
// so a missing registration is not misreported as a measured cache miss.
type SubtitleCacheFacts struct {
	ContentCacheHit  bool
	ArtifactCacheHit bool
	Measured         bool
}

// subtitleCacheFacts is the run-local registry keyed by the materialized ASS
// path. It is populated by the SubtitleCompiler adapter and projected into the
// job result so the benchmark report can show subtitle cache hits.
var subtitleCacheFacts sync.Map // map[string]SubtitleCacheFacts

// RecordSubtitleCacheFacts registers cache ownership for a materialized ASS
// artifact. Called by the SubtitleCompiler adapter after compile.
func RecordSubtitleCacheFacts(localPath string, facts SubtitleCacheFacts) {
	subtitleCacheFacts.Store(localPath, facts)
}

// subtitleCacheFactsFor returns the recorded cache facts for a materialized
// ASS artifact, or an all-false zero value when nothing was recorded.
func subtitleCacheFactsFor(path string) SubtitleCacheFacts {
	if facts, ok := subtitleCacheFacts.Load(path); ok {
		return facts.(SubtitleCacheFacts)
	}
	return SubtitleCacheFacts{}
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

func materializationResult(asset *MaterializedAsset) map[string]any {
	if asset == nil {
		return nil
	}
	// A cache hit means no download bytes were consumed by materialization.
	downloadBytes := asset.SizeBytes
	if asset.FromCache {
		downloadBytes = 0
	}
	return map[string]any{
		"asset_id":       asset.AssetID,
		"path":           asset.LocalPath,
		"sha256":         asset.SHA256,
		"size_bytes":     asset.SizeBytes,
		"from_cache":     asset.FromCache,
		"cache_hit":      asset.FromCache,
		"download_bytes": downloadBytes,
		"materialized":   true,
	}
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

// projectRendererPhases projects the renderer-owned phase timings from the
// canonical V2 report onto the Run bound to ctx as typed operations (e.g.
// subtitle_raster_ms → Run.Operation{operation=subtitle_raster,
// component=chronon, duration_ms}). Every phase was MEASURED by the render
// engine boundary (Chronon/CUDA/FFmpeg) and reported in the canonical clip
// report; the kernel records the owner-reported duration via RecordOperation
// and never re-times the boundary (one boundary = one timer). Phases that
// were not instrumented stay absent — never a fake zero. The component
// mirrors the backend that actually ran, so a software-fallback render is
// never mislabelled as the GPU engine.
func projectRendererPhases(ctx context.Context, backend RenderBackend, m *RenderMetricsV2) {
	if m == nil {
		return
	}
	component := componentForBackend(backend)
	record := func(op kernobs.OperationName, ms Metric) {
		if int64(ms) == NotInstrumented {
			return
		}
		kernobs.RecordOperation(ctx, kernobs.OperationInfo{
			Stage:     StageClipRender,
			Component: component,
			Operation: op,
		}, int64(ms))
	}
	record(kernobs.OperationRendererStartup, m.RendererStartupMS)
	record(kernobs.OperationProbe, m.ProbeMS)
	record(kernobs.OperationDecode, m.DecodeMS)
	record(kernobs.OperationComposite, m.CompositeMS)
	record(kernobs.OperationSubtitleRaster, m.SubtitleRasterMS)
	record(kernobs.OperationWatermarkRaster, m.WatermarkRasterMS)
	record(kernobs.OperationFrameConversion, m.FrameConversionMS)
	record(kernobs.OperationEncode, m.EncodeMS)
	record(kernobs.OperationAudioMux, m.AudioMuxMS)
	// GPU byte counters are counters, not durations: projected with Bytes so
	// the operation never fakes a duration for a non-time measurement.
	recordBytes := func(op kernobs.OperationName, bytes Metric) {
		if int64(bytes) == NotInstrumented {
			return
		}
		kernobs.RecordOperation(ctx, kernobs.OperationInfo{
			Stage:     StageClipRender,
			Component: component,
			Operation: op,
			Bytes:     int64(bytes),
		}, 0)
	}
	recordBytes(kernobs.OperationGPUCopy, m.GPUCopyBytes)
	recordBytes(kernobs.OperationGPUReadback, m.GPUReadbackBytes)
}

// componentForBackend maps the resolved render backend onto the canonical
// component that owns its measured phases. chronon_vulkan (primary) → chronon;
// cuda_native → cuda; ffmpeg_fallback → ffmpeg. Unknown backends keep the
// primary GPU component — a wrong label is never fabricated.
func componentForBackend(b RenderBackend) kernobs.ComponentName {
	switch b {
	case BackendCudaNative:
		return kernobs.ComponentCUDA
	case BackendFFmpegFallback:
		return kernobs.ComponentFFmpeg
	default:
		return kernobs.ComponentChronon
	}
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
