package cliprender

// chronon_metrics_adapter.go — the Chronon Metrics Adapter. It promotes the
// fine-grained phases of Chronon's timing sidecar into the canonical durable
// performance registry (performance_operations) through the
// kernobs.OperationReportProjectionRecorder seam — the same sink the
// ObservedExecutor and the multilingual recorder use. It never writes the
// table directly and never re-times a phase Chronon already measured.
//
// AUTHORITY VS PROJECTION: the kernel RunReport bound to the execution
// context is the operational authority; it already receives the engine's
// canonical phases via projectRendererPhases (renderer_startup, decode,
// composite, ... from the V2 report). This adapter is deliberately
// PROJECTION-ONLY: the exclusive-wall phases (chronon.startup, ...) are the
// engine's internal breakdown with no counterpart in the run-operation
// taxonomy, so they are promoted ONLY to the performance registry — never
// re-recorded on the RunReport, where they would double-count the render
// wall against the V2 projection. The connection to the canonical
// persistence path is exactly the ObservedExecutor's: one measured boundary
// → one OperationReport → the OperationReportProjectionRecorder seam →
// SQLite, run_id/job_id resolved from the canonical run bound to ctx.
//
// The rule this adapter implements: the sidecar JSON is a transport/debug
// payload; the SQLite performance registry is the canonical metrics history
// (normative contract: docs/architecture/chronon-metrics-canonical-store-contract.md).
// One measured sidecar phase → one performance_operations row, so the
// cold/warm comparison (and the future expected_work_ms estimation) queries
// the registry instead of parsing files under /tmp. Publishing happens for
// every job run automatically: the executor invokes Publish during the
// render with the run-bound context, and a row without a canonical run is
// rejected by the store (fail-closed, logged, never a render failure).

import (
	"context"
	"encoding/json"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

// Chronon performance-registry operation names. They are namespaced by
// engine ("chronon.") so the registry can aggregate engine phases without
// colliding with the canonical run-operation taxonomy (renderer_startup,
// encode, ...) used for run_operation_observations.
const (
	ChrononOperationStartup      = "chronon.startup"
	ChrononOperationInputOpen    = "chronon.input_open"
	ChrononOperationPrepare      = "chronon.prepare"
	ChrononOperationRenderLoop   = "chronon.render_loop"
	ChrononOperationEncoderDrain = "chronon.encoder_drain"
	ChrononOperationFFprobe      = "chronon.ffprobe"
	ChrononOperationSHA256       = "chronon.sha256"
)

// ChrononMetricsPublishOptions carries the facts PipelineGen knows about the
// attempt that the sidecar itself does not carry. DaemonReused and
// RendererCreated are lifecycle facts of the CLI invocation (daemon
// warm-renderer reuse is a process event, not a sidecar field): nil means
// unknown and the key is omitted from metadata_json — a value is never
// fabricated. The certified output facts fill the typed
// performance_operations columns (source_sha256, source_duration_ms,
// output_size_bytes, width, height, fps).
type ChrononMetricsPublishOptions struct {
	DaemonReused    *bool
	RendererCreated *bool

	SourceSHA256     string
	SourceDurationMS int64
	OutputSizeBytes  int64
	Width            int
	Height           int
	FPS              float64
}

// ChrononMetricsAdapter projects a ChrononSidecar into canonical
// OperationReports and records them through the projection seam. It is
// safe for concurrent use: it holds no per-attempt state.
type ChrononMetricsAdapter struct {
	recorder kernobs.OperationReportProjectionRecorder
	log      *zap.Logger
}

// NewChrononMetricsAdapter constructs the adapter. Fail-open at record time
// (a metric write must never fail the render it measures): a nil recorder or
// a nil logger is tolerated, with nil log replaced by a no-op.
func NewChrononMetricsAdapter(recorder kernobs.OperationReportProjectionRecorder, log *zap.Logger) *ChrononMetricsAdapter {
	if log == nil {
		log = zap.NewNop()
	}
	return &ChrononMetricsAdapter{recorder: recorder, log: log}
}

// Project converts the sidecar's measured exclusive-wall phases into
// canonical OperationReports (one per measured phase, in the exporter's
// timeline order). Phases the sidecar did not measure stay absent — never a
// fake zero. A phase measured at 0.0 IS published (a real measurement that
// nothing happened). Every report carries the same structured
// metadata_json so a query can group by backend/decoder/encoder/daemon
// reuse/cache context.
func (a *ChrononMetricsAdapter) Project(sc *ChrononSidecar, opts ChrononMetricsPublishOptions) []kernobs.OperationReport {
	if sc == nil {
		return nil
	}
	meta := chrononAttemptMetadata(sc, opts)
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)

	phases := []struct {
		operation string
		ms        *float64
	}{
		{ChrononOperationStartup, sc.StartupMS},
		{ChrononOperationInputOpen, sc.InputOpenMS},
		{ChrononOperationPrepare, sc.PrepareMS},
		{ChrononOperationRenderLoop, sc.RenderLoopMS},
		{ChrononOperationEncoderDrain, sc.EncoderDrainFinalizeMS},
		{ChrononOperationFFprobe, sc.FFprobeMS},
		{ChrononOperationSHA256, sc.SHA256MS},
	}
	reports := make([]kernobs.OperationReport, 0, len(phases))
	for _, phase := range phases {
		if phase.ms == nil {
			continue
		}
		reports = append(reports, kernobs.OperationReport{
			ObservationID:    kernobs.NewObservationID(),
			Stage:            string(StageClipRender),
			Component:        string(kernobs.ComponentChronon),
			Operation:        phase.operation,
			Status:           kernobs.StageStatusCompleted,
			DurationMs:       msFromFloat(*phase.ms),
			SourceSHA256:     opts.SourceSHA256,
			SourceDurationMS: opts.SourceDurationMS,
			OutputSizeBytes:  opts.OutputSizeBytes,
			Width:            opts.Width,
			Height:           opts.Height,
			FPS:              opts.FPS,
			MetadataJSON:     meta,
			CreatedAt:        createdAt,
		})
	}
	return reports
}

// Publish records every projected phase through the canonical
// OperationReportProjectionRecorder. Best-effort by design: a write failure
// is a logged warning, never a render failure (metrics must never fail the
// render they measure). A nil adapter or nil recorder is a no-op.
func (a *ChrononMetricsAdapter) Publish(ctx context.Context, sc *ChrononSidecar, opts ChrononMetricsPublishOptions) {
	if a == nil || a.recorder == nil {
		return
	}
	for _, report := range a.Project(sc, opts) {
		if err := a.recorder.RecordOperationReport(ctx, report); err != nil {
			a.log.Warn("chronon metrics: record operation",
				zap.String("operation", report.Operation),
				zap.String("component", report.Component),
				zap.Int64("duration_ms", report.DurationMs),
				zap.Error(err),
			)
		}
	}
}

// msFromFloat rounds a sidecar float millisecond value to int64, matching the
// exporter's half-up convention used by sumMeasuredMS.
func msFromFloat(ms float64) int64 {
	return int64(ms + 0.5)
}

// chrononAttemptMetadata builds the structured metadata_json shared by every
// phase row of one attempt. Only facts that are actually known are included:
// a "unknown" backend sentinel, a nil byte counter or a nil lifecycle flag
// never appears (NO-FAKE-AVAILABILITY).
func chrononAttemptMetadata(sc *ChrononSidecar, opts ChrononMetricsPublishOptions) string {
	meta := make(map[string]any)
	if sc.Backend != "" && sc.Backend != "unknown" {
		meta["backend"] = sc.Backend
	}
	if sc.Decoder != "" && sc.Decoder != "unknown" {
		meta["decoder"] = sc.Decoder
	}
	if sc.Encoder != "" && sc.Encoder != "unknown" {
		meta["encoder"] = sc.Encoder
	}
	if opts.DaemonReused != nil {
		meta["daemon_reused"] = *opts.DaemonReused
	}
	if opts.RendererCreated != nil {
		meta["renderer_created"] = *opts.RendererCreated
	}
	if sc.GPUAssetCacheHits != nil || sc.GPUAssetCacheMisses != nil {
		var hits, misses uint64
		if sc.GPUAssetCacheHits != nil {
			hits = *sc.GPUAssetCacheHits
		}
		if sc.GPUAssetCacheMisses != nil {
			misses = *sc.GPUAssetCacheMisses
		}
		meta["asset_cache_hit"] = hits > 0
		meta["gpu_asset_cache_hits"] = hits
		meta["gpu_asset_cache_misses"] = misses
	}
	if sc.GPUUploadBytes != nil {
		meta["cuda_upload_bytes"] = *sc.GPUUploadBytes
	}
	if sc.GPUReadbackBytes != nil {
		meta["cuda_readback_bytes"] = *sc.GPUReadbackBytes
	}
	if sc.EncoderStagingCopyBytes != nil {
		meta["encoder_staging_copy_bytes"] = *sc.EncoderStagingCopyBytes
	}
	if sc.ProcessWallMS != nil && *sc.ProcessWallMS > 0 {
		meta["process_wall_ms"] = msFromFloat(*sc.ProcessWallMS)
	}
	if sc.AccountedPercent != nil {
		meta["accounted_percent"] = *sc.AccountedPercent
	}
	if sc.EndToEndFPS != nil {
		meta["end_to_end_fps"] = *sc.EndToEndFPS
	}
	if sc.RenderLoopFPS != nil {
		meta["render_loop_fps"] = *sc.RenderLoopFPS
	}
	if sc.RealtimeFactor != nil {
		meta["realtime_factor"] = *sc.RealtimeFactor
	}
	if sc.GraphReusedFrames != nil {
		meta["graph_reused_frames"] = *sc.GraphReusedFrames
	}
	if sc.FastPathReusedFrames != nil {
		meta["fast_path_reused_frames"] = *sc.FastPathReusedFrames
	}
	if len(meta) == 0 {
		return "{}"
	}
	b, err := json.Marshal(meta)
	if err != nil {
		// A map of JSON-safe scalars can never fail to marshal; the empty
		// document is the honest fallback.
		return "{}"
	}
	return string(b)
}
