package performance

// cold_warm.go owns the platform adapter for the comparative cold #1 vs warm
// #2-N report. It is a pure read projection over performance_operations: the
// GROUP BY operation aggregates (AVG/MIN/MAX elapsed_ms) are computed in
// SQLite from the measured rows, split by attempt position — the first
// measured attempt of the scope is cold, the following attempts are warm.
// Nothing is re-measured and nothing is guessed from cache flags.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
)

var _ capperformance.ColdWarmSource = (*OperationStore)(nil)

// ColdWarmComparison ranks the measured attempts (distinct run_id) in scope by
// first-seen (created_at, then insertion order) and buckets attempt #1 as cold
// and attempts #2..MaxAttempts as warm, returning per-operation AVG/MIN/MAX
// elapsed_ms for each bucket. MaxAttempts defaults to 5 (the certified battery
// shape: cold #1, warm #2..5). Zero/negative elapsed rows never rank an
// attempt: an attempt is defined by having at least one measured phase.
func (s *OperationStore) ColdWarmComparison(ctx context.Context, opts capperformance.ColdWarmOptions) (capperformance.ColdWarmComparison, error) {
	if s == nil || s.db == nil {
		return capperformance.ColdWarmComparison{}, ErrNotWired
	}
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}

	filter := ""
	var filterArgs []any
	if strings.TrimSpace(opts.JobID) != "" {
		filter += " AND job_id = ?"
		filterArgs = append(filterArgs, opts.JobID)
	}
	if strings.TrimSpace(opts.Since) != "" {
		filter += " AND created_at >= ?"
		filterArgs = append(filterArgs, opts.Since)
	}

	// Total measured attempts in scope (before the max-attempts cap).
	var totalAttempts int
	if err := s.db.QueryRowContext(ctx, `
		WITH attempts AS (
			SELECT run_id FROM performance_operations
			WHERE elapsed_ms > 0`+filter+`
			GROUP BY run_id
		)
		SELECT COUNT(*) FROM attempts`, filterArgs...).Scan(&totalAttempts); err != nil {
		return capperformance.ColdWarmComparison{}, fmt.Errorf("cold/warm comparison: count attempts: %w", err)
	}
	attempts := totalAttempts
	if attempts > maxAttempts {
		attempts = maxAttempts
	}

	out := capperformance.ColdWarmComparison{JobID: opts.JobID, Attempts: attempts}
	if attempts > 0 {
		out.ColdAttempts = 1
		out.WarmAttempts = attempts - 1
	}
	if totalAttempts == 0 {
		return out, nil
	}

	args := append(append([]any{}, filterArgs...), maxAttempts)
	rows, err := s.db.QueryContext(ctx, `
		WITH attempts AS (
			SELECT run_id,
				MIN(created_at) AS first_seen,
				MIN(rowid)      AS first_rowid
			FROM performance_operations
			WHERE elapsed_ms > 0`+filter+`
			GROUP BY run_id
		),
		ranked AS (
			SELECT run_id,
				ROW_NUMBER() OVER (ORDER BY first_seen, first_rowid) AS attempt_no
			FROM attempts
		),
		bucketed AS (
			SELECT po.operation, po.elapsed_ms,
				CASE WHEN r.attempt_no = 1 THEN 'cold' ELSE 'warm' END AS bucket
			FROM performance_operations po
			JOIN ranked r ON r.run_id = po.run_id
			WHERE po.elapsed_ms > 0
				AND r.attempt_no <= ?
		)
		SELECT bucket, operation, COUNT(*), AVG(elapsed_ms), MIN(elapsed_ms), MAX(elapsed_ms)
		FROM bucketed
		GROUP BY bucket, operation
		ORDER BY operation, bucket`, args...)
	if err != nil {
		return capperformance.ColdWarmComparison{}, fmt.Errorf("cold/warm comparison: query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			bucket, operation string
			count             int
			avg               sql.NullFloat64
			minV, maxV        sql.NullInt64
		)
		if err := rows.Scan(&bucket, &operation, &count, &avg, &minV, &maxV); err != nil {
			return capperformance.ColdWarmComparison{}, fmt.Errorf("cold/warm comparison: scan: %w", err)
		}
		b := capperformance.OperationBucket{
			Operation:    operation,
			Runs:         count,
			AvgElapsedMS: avg.Float64,
			MinElapsedMS: minV.Int64,
			MaxElapsedMS: maxV.Int64,
		}
		if bucket == "cold" {
			out.Cold = append(out.Cold, b)
		} else {
			out.Warm = append(out.Warm, b)
		}
	}
	if err := rows.Err(); err != nil {
		return capperformance.ColdWarmComparison{}, fmt.Errorf("cold/warm comparison: iterate: %w", err)
	}
	return out, nil
}
