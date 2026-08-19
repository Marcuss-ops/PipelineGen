// Package performance — operations.go owns the platform adapter for
// per-operation media performance (table performance_operations, migration
// 217). It is the analytics projection sink for the ObservedExecutor's
// canonical kernel observation and the query-side OperationAnalytics (the dashboard /
// benchmark comparison answer to "what does each operation cost").
//
// One kernel MeasuredOperation → one performance_operations row. run_id/job_id
// are resolved from the kernobs run bound to the request context; an operation
// outside a tracked run is rejected. step_id has no canonical context spelling
// today and stays "". The Real-Time Factor (elapsed / source
// duration) is DERIVED in OperationStats — never stored.
package performance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

var ErrNotWired = errors.New("performance operations store: not wired")

type OperationStore struct{ db *sql.DB }

// NewOperationStore constructs the adapter. Fail-closed: a nil database is a
// construction error.
func NewOperationStore(db *sql.DB) (*OperationStore, error) {
	if db == nil {
		return nil, ErrNotWired
	}
	return &OperationStore{db: db}, nil
}

var _ kernobs.OperationReportProjectionRecorder = (*OperationStore)(nil)
var _ capperformance.OperationAnalytics = (*OperationStore)(nil)
var _ capperformance.BenchmarkSource = (*OperationStore)(nil)

// RecordOperationReport projects the canonical operation fact into the
// performance read model. It never measures time and never accepts an
// operation without a canonical run identity.
func (s *OperationStore) RecordOperationReport(ctx context.Context, m kernobs.OperationReport) error {
	if err := validateMeasurement(m); err != nil {
		return err
	}
	// This adapter is projection-only. A performance row without a canonical
	// run observation is an orphaned second source of truth, so reject calls
	// outside a run instead of inventing empty identity fields.
	runID, jobID, stepID := resolveIdentity(ctx)
	if runID == "" || jobID == "" {
		return errors.New("performance operations: canonical run context is required")
	}
	cacheHit := 0
	if m.CacheHit {
		cacheHit = 1
	}
	createdAt := m.CreatedAt
	if strings.TrimSpace(createdAt) == "" {
		createdAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO performance_operations
		(operation_id, run_id, job_id, step_id, operation, source_sha256, source_duration_ms, source_size_bytes,
		 width, height, fps, input_codec, output_codec, elapsed_ms, cpu_user_ms, cpu_system_ms,
		 output_size_bytes, cache_hit, strategy, metadata_json, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(operation_id) DO UPDATE SET
			run_id=excluded.run_id, job_id=excluded.job_id, step_id=excluded.step_id, operation=excluded.operation,
			source_sha256=excluded.source_sha256, source_duration_ms=excluded.source_duration_ms, source_size_bytes=excluded.source_size_bytes,
			width=excluded.width, height=excluded.height, fps=excluded.fps, input_codec=excluded.input_codec, output_codec=excluded.output_codec,
			elapsed_ms=excluded.elapsed_ms, cpu_user_ms=excluded.cpu_user_ms, cpu_system_ms=excluded.cpu_system_ms,
			output_size_bytes=excluded.output_size_bytes, cache_hit=excluded.cache_hit, strategy=excluded.strategy,
			metadata_json=excluded.metadata_json, created_at=excluded.created_at`,
		m.ObservationID, runID, jobID, stepID, m.Operation,
		m.SourceSHA256, m.SourceDurationMS, m.SourceSizeBytes,
		m.Width, m.Height, m.FPS, m.InputCodec, m.OutputCodec,
		m.DurationMs, m.CPUUserMS, m.CPUSystemMS,
		m.OutputSizeBytes, cacheHit, m.Strategy, nonEmpty(m.MetadataJSON, "{}"), createdAt)
	if err != nil {
		return fmt.Errorf("record performance operation %q: %w", m.Operation, err)
	}
	return nil
}

// OperationStats returns per-operation aggregates (runs, avg elapsed/output,
// cache hits, avg source duration) with the derived AvgRTF = avg elapsed /
// avg source duration. An empty since is "all recorded operations".
func (s *OperationStore) OperationStats(ctx context.Context, since string) ([]capperformance.OperationStats, error) {
	query := `SELECT operation, COUNT(*), AVG(elapsed_ms), AVG(output_size_bytes), SUM(cache_hit), AVG(source_duration_ms)
		FROM performance_operations`
	var args []any
	if strings.TrimSpace(since) != "" {
		query += ` WHERE created_at >= ?`
		args = append(args, since)
	}
	query += ` GROUP BY operation ORDER BY operation`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("performance operation stats: %w", err)
	}
	defer rows.Close()

	var stats []capperformance.OperationStats
	for rows.Next() {
		var (
			st         capperformance.OperationStats
			avgElapsed sql.NullFloat64
			avgOutput  sql.NullFloat64
			cacheHits  sql.NullInt64
			avgSource  sql.NullFloat64
		)
		if err := rows.Scan(&st.Operation, &st.Runs, &avgElapsed, &avgOutput, &cacheHits, &avgSource); err != nil {
			return nil, fmt.Errorf("performance operation stats: scan: %w", err)
		}
		st.AvgElapsedMS = avgElapsed.Float64
		st.AvgOutputSizeBytes = avgOutput.Float64
		st.CacheHits = cacheHits.Int64
		st.AvgSourceDurationMS = avgSource.Float64
		if st.AvgSourceDurationMS > 0 {
			// RTF = elapsed / source duration (derived, never stored).
			// < 1 means faster than realtime.
			st.AvgRTF = st.AvgElapsedMS / st.AvgSourceDurationMS
		}
		stats = append(stats, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("performance operation stats: iterate: %w", err)
	}
	return stats, nil
}

// OperationSamples returns the canonical elapsed_ms samples for one operation,
// oldest first — the benchmark baseline derived from performance_operations
// (the ObservedExecutor's single boundary measurement). It never re-measures
// wall time at the read boundary.
func (s *OperationStore) OperationSamples(ctx context.Context, operation, since string) ([]int64, error) {
	if strings.TrimSpace(operation) == "" {
		return nil, errors.New("performance operation samples: operation is required")
	}
	query := `SELECT elapsed_ms FROM performance_operations WHERE operation = ?`
	var args []any
	args = append(args, operation)
	if strings.TrimSpace(since) != "" {
		query += ` AND created_at >= ?`
		args = append(args, since)
	}
	// rowid is the deterministic insertion-order tiebreaker: elapsed_ms rows
	// written by the ObservedExecutor in the same created_at second still
	// return in sample order.
	query += ` ORDER BY created_at, rowid`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("performance operation samples %q: %w", operation, err)
	}
	defer rows.Close()
	var samples []int64
	for rows.Next() {
		var elapsed int64
		if err := rows.Scan(&elapsed); err != nil {
			return nil, fmt.Errorf("performance operation samples %q: scan: %w", operation, err)
		}
		samples = append(samples, elapsed)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("performance operation samples %q: iterate: %w", operation, err)
	}
	return samples, nil
}

// BenchmarkStats derives per-operation benchmark aggregates (sample count,
// median elapsed, median source duration, derived median RTF, cache hits) from
// the canonical performance_operations rows. Medians are computed in Go over
// the canonical elapsed_ms / source_duration_ms samples — never re-measured
// at the read boundary. MedianRTF is 0 when the operation has no measured
// source duration.
func (s *OperationStore) BenchmarkStats(ctx context.Context, since string) ([]capperformance.BenchmarkStats, error) {
	query := `SELECT operation, elapsed_ms, source_duration_ms, cache_hit FROM performance_operations`
	var args []any
	if strings.TrimSpace(since) != "" {
		query += ` WHERE created_at >= ?`
		args = append(args, since)
	}
	query += ` ORDER BY operation, created_at`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("performance benchmark stats: query: %w", err)
	}
	defer rows.Close()

	type accumulator struct {
		elapsed   []int64
		source    []int64
		cacheHits int64
	}
	var order []string
	byOperation := make(map[string]*accumulator)
	for rows.Next() {
		var (
			operation string
			elapsed   int64
			source    int64
			cacheHit  int64
		)
		if err := rows.Scan(&operation, &elapsed, &source, &cacheHit); err != nil {
			return nil, fmt.Errorf("performance benchmark stats: scan: %w", err)
		}
		acc, ok := byOperation[operation]
		if !ok {
			acc = &accumulator{}
			byOperation[operation] = acc
			order = append(order, operation)
		}
		acc.elapsed = append(acc.elapsed, elapsed)
		acc.source = append(acc.source, source)
		acc.cacheHits += cacheHit
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("performance benchmark stats: iterate: %w", err)
	}

	stats := make([]capperformance.BenchmarkStats, 0, len(order))
	for _, operation := range order {
		acc := byOperation[operation]
		st := capperformance.BenchmarkStats{
			Operation:       operation,
			Samples:         int64(len(acc.elapsed)),
			MedianElapsedMS: capperformance.MedianInt64(acc.elapsed),
			MedianSourceMS:  capperformance.MedianInt64(acc.source),
			CacheHits:       acc.cacheHits,
		}
		if st.MedianSourceMS > 0 {
			// Median RTF = median elapsed / median source duration (derived,
			// never stored; < 1 means faster than realtime).
			st.MedianRTF = st.MedianElapsedMS / st.MedianSourceMS
		}
		stats = append(stats, st)
	}
	return stats, nil
}

// validateMeasurement fails closed on an anonymous operation: aggregation is
// meaningless without an operation name. Everything else is optional.
func validateMeasurement(m kernobs.OperationReport) error {
	if strings.TrimSpace(m.Operation) == "" {
		return errors.New("performance operations: operation is required")
	}
	if strings.TrimSpace(m.ObservationID) == "" {
		return errors.New("performance operations: observation id is required")
	}
	return nil
}

// resolveIdentity reads the run correlation from the kernobs run bound to
// the context. step_id has no canonical context spelling today and stays "".
func resolveIdentity(ctx context.Context) (runID, jobID, stepID string) {
	if run := kernobs.FromContext(ctx); run != nil {
		if rep := run.Report(); rep != nil {
			runID, jobID = rep.RunID, rep.JobID
		}
	}
	return runID, jobID, ""
}
