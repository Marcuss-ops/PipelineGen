package performance

// work_estimator_reader.go — the adapter that turns the durable performance
// history (performance_operations) into the Preparation Fabric's
// work-estimator feed. It is the scheduler-intelligence rule in code: what
// the execution layer measured yesterday becomes expected_work_ms for
// tomorrow's jobs.
//
// The adapter maps measured operation names onto preparation unit kinds (the
// estimator keys on job.UnitKind), derives the workload driver from the
// row's measured facts (reusing job.PreparationUnit.Driver — the SSOT for
// kind→axis) and emits one job.WorkObservation per measured row. It
// structurally satisfies internal/capabilities/jobs.WorkObservationsReader,
// so the jobs-side estimator consumes it without importing this package's
// types. Rows with elapsed_ms <= 0 are dropped by the store query; cache-hit
// rows are kept (a hit still costs the observed wall time) exactly like the
// preparation_attempts feed.

import (
	"context"
	"fmt"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// performanceOperationKinds maps measured operation names onto the
// preparation unit kinds the estimator learns for. The default (absent
// entry) is identity — the operation name IS the unit kind — so every
// measured operation contributes signal under its own name. The explicit
// entries exist where a fine-grained measurement must feed a coarser unit:
// the Chronon render loop is THE render cost, so it lands under the render
// kind (frames-scaled by job.PreparationUnit.Driver) instead of polluting
// the kind EMA with startup/prepare/drain overhead.
var performanceOperationKinds = map[string]job.UnitKind{
	"chronon.render_loop": "chronon.render",
}

// WorkHistoryReader adapts a WorkHistorySource into
// jobs.WorkObservationsReader-shaped observations. It is stateless and safe
// for concurrent use.
type WorkHistoryReader struct {
	source WorkHistorySource
}

// NewWorkHistoryReader builds the adapter. A nil source is tolerated: the
// reader then yields an empty observation set (fail-open).
func NewWorkHistoryReader(source WorkHistorySource) *WorkHistoryReader {
	return &WorkHistoryReader{source: source}
}

// ListPreparationWorkObservations projects the durable performance history
// into work observations, newest first. A nil/unavailable source returns an
// empty set; a read error is returned so the caller can decide (the jobs
// estimator's Bootstrap is fail-open).
func (r *WorkHistoryReader) ListPreparationWorkObservations(ctx context.Context, limit int) ([]job.WorkObservation, error) {
	if r == nil || r.source == nil {
		return []job.WorkObservation{}, nil
	}
	rows, err := r.source.ListWorkHistory(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("performance history work observations: %w", err)
	}
	out := make([]job.WorkObservation, 0, len(rows))
	for i := range rows {
		kind := mappedPerformanceKind(rows[i].Operation)
		dimension, amount := performanceWorkloadDriver(kind, rows[i])
		out = append(out, job.WorkObservation{
			Kind:      kind,
			WallMS:    rows[i].ElapsedMS,
			Dimension: dimension,
			Amount:    amount,
		})
	}
	return out, nil
}

// mappedPerformanceKind resolves the unit kind for a measured operation:
// explicit table first, identity otherwise.
func mappedPerformanceKind(operation string) job.UnitKind {
	if kind, ok := performanceOperationKinds[operation]; ok {
		return kind
	}
	return job.UnitKind(operation)
}

// performanceWorkloadDriver derives the scaling axis + amount for a measured
// row from the row's measured facts, reusing job.PreparationUnit.Driver (the
// SSOT for kind→axis). Frames come from duration × fps; bytes from the
// source size. Kinds whose axis needs chars/tokens (TTS, LLM) get Dimension
// none here — the performance registry carries no text — and the estimator
// falls back to the per-kind average EMA.
func performanceWorkloadDriver(kind job.UnitKind, row WorkHistoryRow) (job.WorkloadDimension, float64) {
	inputs := job.InputManifest{}
	if row.SourceDurationMS > 0 && row.FPS > 0 {
		frames := float64(row.SourceDurationMS) / 1000.0 * row.FPS
		// Driver probes both spellings; supply both so any future axis
		// reading stays unambiguous.
		inputs["frames"] = frames
		inputs["frame_count"] = frames
	}
	if row.SourceSizeBytes > 0 {
		inputs["bytes"] = float64(row.SourceSizeBytes)
		inputs["size_bytes"] = float64(row.SourceSizeBytes)
	}
	driver := (job.PreparationUnit{Kind: kind, Inputs: inputs}).Driver()
	return driver.Dimension, driver.Amount
}
