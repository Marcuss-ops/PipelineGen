// Package rustexec — observed_executor.go owns the single measurement point
// around the Rust media executor:
//
//	Go
//	  ↓
//	MediaExecutor (Client.call)
//	  ↓
//	ObservedExecutor   ← every operation passes through here
//	  ↓
//	Rust
//
// Every operation (probe, normalize, cut, watermark, render, mux, ...)
// produces exactly ONE canonical OperationReport. The boundary measurement is
// promoted and recorded here — never scattered across operation handlers. Wall time is measured in
// Go at the boundary; CPU time, frames and authoritative byte counts come
// from the Rust metrics block (measured in the process that owns the work,
// i.e. the child FFmpeg). A metric write failure never fails the operation.
package rustexec

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"go.uber.org/zap"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// ObservedExecutor decorates a Client with per-operation measurement. It is
// the canonical ObservedMediaExecutor seam: the wrapper holds the single
// recorder and the single timing point.
type ObservedExecutor struct {
	next     *Client
	recorder kernobs.OperationReportProjectionRecorder
	log      *zap.Logger
}

// NewObservedExecutor builds the decorator. A nil recorder disables
// recording (the wrapper still times the operation); a nil logger is a
// no-op logger.
func NewObservedExecutor(next *Client, recorder kernobs.OperationReportProjectionRecorder) *ObservedExecutor {
	return &ObservedExecutor{next: next, recorder: recorder, log: zap.NewNop()}
}

// SetLogger attaches a logger for metric-write failures (nil-safe no-op).
func (o *ObservedExecutor) SetLogger(log *zap.Logger) {
	if o != nil && log != nil {
		o.log = log
	}
}

// Execute runs one operation through the wrapped client and records exactly
// one measurement (also on error). The operation's result/error are returned
// unchanged; a metric write failure is logged, never propagated.
func (o *ObservedExecutor) Execute(ctx context.Context, req request) (response, error) {
	started := time.Now()
	result, err := o.next.execute(ctx, req)
	measurement := o.measurement(req, started, result, err)
	measurement.ObservationID = kernobs.NewObservationID()
	// The run-bound report is the operational authority. Any configured
	// performance recorder below is a projection sink only.
	kernobs.RecordMeasuredOperation(ctx, measurement)
	if o != nil && o.recorder != nil {
		if recordErr := o.recorder.RecordOperationReport(ctx, kernobs.OperationReportFromMeasuredOperation(measurement)); recordErr != nil {
			if o.log != nil {
				o.log.Warn("operation metric write failed",
					zap.String("operation", string(req.Operation)),
					zap.Error(recordErr))
			}
		}
	}
	return result, err
}

// measurement assembles the operation's measured facts. Go measures wall
// time and input/output bytes at the boundary; the Rust metrics block (when
// present) supplies CPU time, byte counts measured at the source, cache
// outcome and frame counts.
func (o *ObservedExecutor) measurement(req request, started time.Time, result response, err error) kernobs.MeasuredOperation {
	m := kernobs.MeasuredOperation{
		Operation:       string(req.Operation),
		Width:           int(req.Width),
		Height:          int(req.Height),
		FPS:             wireFPSFloat(req.FPSNum, req.FPSDen),
		OutputCodec:     req.Codec,
		ElapsedMS:       time.Since(started).Milliseconds(),
		SourceSizeBytes: sumInputBytes(req),
		OutputSizeBytes: sumOutputBytes(req),
		MetadataJSON:    "{}",
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}
	if req.DurationSec > 0 {
		m.SourceDurationMS = int64(req.DurationSec * 1000)
	}
	if metrics := result.Metrics; metrics != nil {
		// Preserve measured zeroes; the Rust metrics block is authoritative
		// whenever present. Absent blocks remain uninstrumented at the
		// downstream RenderMetricsV2 projection.
		if metrics.CPUUserMS > 0 {
			m.CPUUserMS = metrics.CPUUserMS
		}
		if metrics.CPUSystemMS > 0 {
			m.CPUSystemMS = metrics.CPUSystemMS
		}
		if metrics.InputBytes > 0 {
			m.SourceSizeBytes = metrics.InputBytes
		}
		if metrics.OutputBytes > 0 {
			m.OutputSizeBytes = metrics.OutputBytes
		}
		m.CacheHit = metrics.CacheHit
		m.MetadataJSON = metricsMetadataJSON(metrics)
	}
	if err != nil {
		// Keep the canonical operation fact even on failure; status/error
		// ownership remains with the RunReport recorder.
		m.MetadataJSON = metricsMetadataJSON(result.Metrics)
	}
	return m
}

// metricsMetadataJSON projects the Rust-reported metrics that have no
// dedicated measurement column (frames, Rust-side wall time) into the
// metadata JSON.
func metricsMetadataJSON(m *OperationMetrics) string {
	if m == nil {
		return "{}"
	}
	b, err := json.Marshal(struct {
		WallMS         int64 `json:"wall_ms"`
		FramesDecoded  int64 `json:"frames_decoded"`
		FramesEncoded  int64 `json:"frames_encoded"`
		PeakRSSBytes   int64 `json:"peak_rss_bytes"`
		DiskReadBytes  int64 `json:"disk_read_bytes"`
		DiskWriteBytes int64 `json:"disk_write_bytes"`
		NetworkRXBytes int64 `json:"network_rx_bytes"`
		NetworkTXBytes int64 `json:"network_tx_bytes"`
	}{m.WallMS, m.FramesDecoded, m.FramesEncoded, m.PeakRSSBytes, m.DiskReadBytes, m.DiskWriteBytes, m.NetworkRXBytes, m.NetworkTXBytes})
	if err != nil {
		return "{}"
	}
	return string(b)
}

// wireFPSFloat projects the wire rational frame rate to the float observation
// column. Zero denominator (absent rate) projects to 0.
func wireFPSFloat(num, den uint32) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

// sumInputBytes totals the request's source files (SourcePath + InputPaths).
// Unreadable files contribute 0 — the boundary never fails an operation for
// a stat failure on a metric.
func sumInputBytes(req request) int64 {
	var total int64
	paths := make([]string, 0, len(req.InputPaths)+1)
	if req.SourcePath != "" {
		paths = append(paths, req.SourcePath)
	}
	paths = append(paths, req.InputPaths...)
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
	}
	return total
}

// sumOutputBytes totals the request's output files (OutputPath + Jobs'
// outputs) after the operation completed. Unreadable files contribute 0.
func sumOutputBytes(req request) int64 {
	var total int64
	if req.OutputPath != "" {
		if info, err := os.Stat(req.OutputPath); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
	}
	for _, job := range req.Jobs {
		if job.OutputPath != "" {
			if info, err := os.Stat(job.OutputPath); err == nil && info.Mode().IsRegular() {
				total += info.Size()
			}
		}
	}
	return total
}
