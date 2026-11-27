package cliprender

// worker_result.go contains the result projection helpers used by the
// clip.render worker. The projection is split by result block so each block
// owns one contract: identity/materialization, contract, plan, preparation,
// media, subtitles, render, overlay provenance and publication.

import (
	"context"
	"strings"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/cacheutil"
)

// renderedResult projects the *Prepared + sealed plan + render outcome +
// declared overlay lineage + published derived asset into the canonical job
// result map. Only JSON-safe values — the result envelope is persisted by the
// Master. There is no composited-overlay projection: the overlay travels in
// the sealed plan and is composited by the render boundary itself.
func renderedResult(j *job.Job, req *RenderRequest, prepared *Prepared, plan ClipRenderPlanV1, subtitleArtifact *SubtitleArtifact, outcome *RenderOutcome, published *RenderPublishResult) job.Result {
	req, prepared = normalizeRenderedInputs(req, prepared)
	result := baseRenderedResult(j, req, prepared, plan)
	applyRenderedContract(result, prepared)
	applyRenderedPlan(result, plan)
	applyRenderedTimings(result, prepared)
	applyRenderedMedia(result, prepared)
	applyRenderedSubtitles(result, subtitleArtifact)
	applyRenderedOutcome(result, outcome)
	applyRenderedOverlay(result, req, plan)
	applyRenderedPublication(result, published)
	return result
}

func normalizeRenderedInputs(req *RenderRequest, prepared *Prepared) (*RenderRequest, *Prepared) {
	if req == nil {
		req = &RenderRequest{Transcript: &TranscriptSpec{}}
	} else if req.Transcript == nil {
		req.Transcript = &TranscriptSpec{}
	}
	if prepared == nil {
		prepared = &Prepared{Contract: &ResolvedContract{}, Transcript: &TranscriptResult{}}
	}
	if prepared.Transcript == nil {
		prepared.Transcript = &TranscriptResult{}
	}
	if prepared.Source == nil {
		prepared.Source = &MaterializedAsset{}
	}
	return req, prepared
}

func baseRenderedResult(j *job.Job, req *RenderRequest, prepared *Prepared, plan ClipRenderPlanV1) job.Result {
	jobID := ""
	if j != nil {
		jobID = j.ID
	}
	return job.Result{
		"job_id":          jobID,
		"source_asset_id": req.SourceAssetID,
		"phase":           "rendered",
		"transcript_mode": req.Transcript.Mode,
		// The source exists once, under materialization.source. The historical
		// top-level alias duplicated the same block (CR-21).
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
	}
}

// The resolved contract is reported only when a contract was actually
// resolved. A deterministic cache hit renders nothing, so it has no contract
// of its own: the historical synthesis put the BACKEND NAME in contract_id and
// emitted an all-zero contract block, i.e. a wire shape that claims a media
// contract the renderer was never held to.
func applyRenderedContract(result job.Result, prepared *Prepared) {
	if prepared == nil {
		return
	}
	if prepared.Contract != nil && prepared.Contract.ID != "" {
		result["contract_id"] = prepared.Contract.ID
		result["contract"] = ContractWireBlock(prepared.Contract)
	}
}

// A sealed plan block is reported only when a plan was actually sealed. A
// deterministic cache hit reuses another render's artifact: it has no plan of
// its own, and the historical synthesis exposed a REQUEST fingerprint in the
// plan_sha256 slot, i.e. a digest that identifies no plan at all.
func applyRenderedPlan(result job.Result, plan ClipRenderPlanV1) {
	if plan.Version == "" && plan.PlanSHA256 == "" {
		return
	}
	backgroundMode := ""
	if plan.Background != nil {
		backgroundMode = plan.Background.Mode
	}
	result["plan"] = map[string]any{
		"version":     plan.Version,
		"plan_sha256": plan.PlanSHA256,
		"output_path": plan.OutputPath,
		"audio_mode":  plan.Audio.Mode,
		"background":  backgroundMode,
	}
}

// Preparation timings are reported only when a preparation phase actually ran.
// A cache hit prepares nothing, and an all-zero block would report a measured
// zero for work nobody did (the package-wide rule is NOT_INSTRUMENTED, never a
// fabricated zero).
func applyRenderedTimings(result job.Result, prepared *Prepared) {
	if len(prepared.Timings.Phases) == 0 && prepared.Timings.TotalWallMS == 0 && prepared.Timings.TotalWorkMS == 0 {
		return
	}
	result["timings"] = map[string]any{
		"total_wall_ms": prepared.Timings.TotalWallMS,
		"total_work_ms": prepared.Timings.TotalWorkMS,
		"parallel":      prepared.Timings.Parallel,
		"phases":        prepared.Timings.Phases,
	}
}

func applyRenderedMedia(result job.Result, prepared *Prepared) {
	if prepared.Watermark != nil {
		result["watermark"] = materializationResult(prepared.Watermark)
	}
	if prepared.Background != nil {
		result["background"] = materializationResult(prepared.Background)
	}
}

func applyRenderedSubtitles(result job.Result, subtitleArtifact *SubtitleArtifact) {
	if subtitleArtifact == nil {
		return
	}
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

func applyRenderedOutcome(result job.Result, outcome *RenderOutcome) {
	if outcome == nil {
		return
	}
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
	}

	if outcome.Metrics != nil {
		// render_wall_ms lives only in the canonical metrics_v2 report.
		// Mirroring it in render.* made one number look like two (CR-21).
		renderBlock["metrics_v2"] = outcome.Metrics
	}
	// The raw deep-profile timing sidecar reference preserved by
	// RenderingGen: a small content-addressed reference on the job result so
	// post-mortems can fetch the full per-frame profile (the array itself is
	// never inlined). Present only when the sidecar was preserved.
	if outcome.ChrononTimingStorageKey != "" || outcome.ChrononTimingURL != "" {
		renderBlock["chronon_timing"] = map[string]any{
			"storage_key":  outcome.ChrononTimingStorageKey,
			"url":          outcome.ChrononTimingURL,
			"sha256":       outcome.ChrononTimingSHA256,
			"size_bytes":   outcome.ChrononTimingSizeBytes,
			"content_type": outcome.ChrononTimingContentType,
			"source":       outcome.ChrononTimingSource,
			"read_bytes":   outcome.ChrononTimingReadBytes,
			"fetch_ms":     outcome.ChrononTimingFetchMS,
		}
	}
	result["render"] = renderBlock
}

func applyRenderedOverlay(result job.Result, req *RenderRequest, plan ClipRenderPlanV1) {
	if len(req.Overlays) == 0 {
		return
	}
	// The overlay is composited inside the render, so the plan is the
	// authority for WHERE every segment landed (each window is part of the
	// plan digest) and the render block is the authority for the output
	// bytes. One entry per declared lineage keeps the provenance of a
	// multi-item clip auditable instead of collapsing it to the first.
	provenance := make([]map[string]any, 0, len(req.Overlays))
	for i, lineage := range req.Overlays {
		entry := map[string]any{
			"render_job_id":         lineage.RenderJobID,
			"plan_fingerprint":      lineage.PlanFingerprint,
			"render_key":            lineage.RenderKey,
			"source_video_asset_id": lineage.SourceVideoAssetID,
			"start_us":              lineage.StartUS,
			"end_us":                lineage.EndUS,
		}
		if plan.Overlay != nil && i < len(plan.Overlay.Segments) {
			seg := plan.Overlay.Segments[i]
			entry["start_ms"] = seg.StartMS
			entry["end_ms"] = seg.EndMS
			entry["segment_sha256"] = seg.SHA256
		}
		provenance = append(provenance, entry)
	}
	// single_pass is a property of the composition, not of one segment: the
	// clip is encoded once with every declared overlay inside it.
	result["overlay"] = map[string]any{
		"single_pass": true,
		"segments":    provenance,
	}
}

func applyRenderedPublication(result job.Result, published *RenderPublishResult) {
	if published == nil {
		return
	}
	deliveryStatus := published.DeliveryStatus
	if deliveryStatus == "" {
		if published.DrivePending {
			deliveryStatus = "pending"
		} else {
			deliveryStatus = "completed"
		}
	}
	publicationStatus := "PUBLISHED"
	switch {
	case published.Reused:
		// A deterministic cache hit: another render published this asset and
		// this job reused its certified locator, so it has no delivery
		// outcome of its own to report.
		publicationStatus = "REUSED"
	case published.DrivePending:
		publicationStatus = "PENDING"
	}
	result["asset"] = map[string]any{
		"asset_id":           published.AssetID,
		"drive_file_id":      published.DriveFileID,
		"drive_link":         published.DriveLink,
		"drive_pending":      published.DrivePending,
		"delivery_status":    deliveryStatus,
		"publication_status": publicationStatus,
		"content_url":        published.ContentURL,
		"size_bytes":         published.SizeBytes,
		"sidecar_link":       published.SidecarLink,
	}
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
//
// The registry is a bounded LRU: facts are read once, shortly after being
// recorded (compile → job result in the same run), so eviction at capacity
// never loses an in-flight read. Bounding replaces the historical unbounded
// sync.Map + full-sweep janitor, which destroyed every recorded fact at once
// every 10 minutes and burned a goroutine + full scan even when the map was
// empty.
const subtitleCacheFactsCapacity = 2048

var subtitleCacheFacts = cacheutil.NewLRU(subtitleCacheFactsCapacity)

// RecordSubtitleCacheFacts registers cache ownership for a materialized ASS
// artifact. Called by the SubtitleCompiler adapter after compile.
func RecordSubtitleCacheFacts(localPath string, facts SubtitleCacheFacts) {
	subtitleCacheFacts.Put(localPath, facts)
}

// subtitleCacheFactsFor returns the recorded cache facts for a materialized
// ASS artifact, or an all-false zero value when nothing was recorded.
func subtitleCacheFactsFor(path string) SubtitleCacheFacts {
	if facts, ok := subtitleCacheFacts.Get(path); ok {
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
		"download_bytes": downloadBytes,
		"materialized":   true,
	}
}

// addQueueWaitMS projects the measured admission wait into the job result.
// It is never a fabricated zero: a missing/zero creation time (or a clock
// inversion) reports NOT_INSTRUMENTED (CR-08).
func addQueueWaitMS(result job.Result, j *job.Job, startedAt time.Time) {
	if result == nil {
		return
	}
	if j == nil || j.CreatedAt.IsZero() || startedAt.Before(j.CreatedAt) {
		result["queue_wait_ms"] = "NOT_INSTRUMENTED"
		return
	}
	result["queue_wait_ms"] = startedAt.Sub(j.CreatedAt).Milliseconds()
}

// finalizeMetrics sets THIS handler's wall time on the V2 report and
// recomputes the derived aggregates (unaccounted_ms, FPS, realtime factor).
//
// On the async path total_ms belongs to the settle job, so it does NOT span
// the request: preparation and submit happened in the parent job. The
// end-to-end measures are request_to_artifact_ms / request_to_delivery_ms,
// applied separately by applyRequestTimings when the continuation carries the
// original acceptance timestamp (CRA-01). Do not describe total_ms as the
// endpoint latency.
func finalizeMetrics(m *RenderMetricsV2, jobTotalMS int64, durationSec float64) {
	if m == nil {
		return
	}
	m.TotalMS = Metric(jobTotalMS)
	m.Compute(durationSec)
}

// applyRequestTimings projects the acceptance-relative walls that the async
// split otherwise hides. Every input is optional: a job written before
// AcceptedAt existed (or a cache hit in the submit phase, where the request
// acceptance is the submit job's own CreatedAt) leaves the corresponding
// metric NOT_INSTRUMENTED instead of guessing.
//
// Clock skew is clamped, not fail-closed: a negative interval is not a
// measurement, and a render must not fail because two processes disagree.
func applyRequestTimings(m *RenderMetricsV2, acceptedAt, submittedAt, jobStart, completedAt time.Time, deliveryStatus string) {
	if m == nil {
		return
	}
	if !jobStart.IsZero() && !completedAt.IsZero() && !completedAt.Before(jobStart) {
		m.SettleWallMS = Metric(completedAt.Sub(jobStart).Milliseconds())
	}
	if acceptedAt.IsZero() || completedAt.IsZero() || completedAt.Before(acceptedAt) {
		return
	}
	artifactMS := completedAt.Sub(acceptedAt).Milliseconds()
	m.RequestToArtifactMS = Metric(artifactMS)
	if !submittedAt.IsZero() && !submittedAt.Before(acceptedAt) {
		m.SubmitWallMS = Metric(submittedAt.Sub(acceptedAt).Milliseconds())
	}
	switch deliveryStatus {
	case "completed", "not_required":
		// The render job observed a terminal delivery: the artifact and the
		// delivery completed inside the same measured interval.
		m.RequestToDeliveryMS = Metric(artifactMS)
	}
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
	recordBytes(kernobs.OperationGPUUpload, m.GPUUploadBytes)
	recordBytes(kernobs.OperationGPUReadback, m.GPUReadbackBytes)
}

// componentForBackend maps the render backend onto the canonical component
// that owns its measured phases. Chronon is the only render backend, so every
// measured render phase belongs to the chronon component. The parameter is
// retained so the projection site stays explicit about which backend produced
// the phases; a wrong label is never fabricated for an unknown value.
func componentForBackend(_ RenderBackend) kernobs.ComponentName {
	return kernobs.ComponentChronon
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
