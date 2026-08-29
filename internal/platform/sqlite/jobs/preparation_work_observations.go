package jobs

import (
	"context"
	"fmt"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// This sister file (AGENTS.md Pattern 5) owns the work-learning read surface:
// ListPreparationWorkObservations. It projects the append-only
// preparation_attempts ledger (migration 247) into the flat observations the
// PreparationWorkEstimator learns from — one row per completed (READY/HIT)
// attempt, joined to the unit registry so the unit_kind is known. It never
// mutates attempt or unit state.

// ListPreparationWorkObservations returns up to limit completed preparation
// attempts (status READY, with a finish time) as observations, newest first,
// joined to the unit kind via the fingerprint. It is the bootstrap feed for the
// per-kind EMA work estimator, replacing static expected_work_ms guesses.
func (r *SQLiteStore) ListPreparationWorkObservations(ctx context.Context, limit int) ([]job.WorkObservation, error) {
	if limit <= 0 {
		limit = 1000
	}
	if limit > 5000 {
		limit = 5000
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT COALESCE(u.unit_kind, ''), a.wall_ms,
			COALESCE(a.workload_dimension, ''), COALESCE(a.workload_amount, 0)
		FROM preparation_attempts a
		LEFT JOIN preparation_units u ON u.fingerprint = a.unit_fingerprint
		WHERE a.status IN ('READY', 'HIT') AND a.wall_ms > 0
		ORDER BY COALESCE(a.finished_at, a.created_at) DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list preparation work observations: %w", err)
	}
	defer rows.Close()

	out := []job.WorkObservation{}
	for rows.Next() {
		var kind string
		var wallMS int64
		var dimension string
		var amount float64
		if err := rows.Scan(&kind, &wallMS, &dimension, &amount); err != nil {
			return nil, fmt.Errorf("scan preparation work observation: %w", err)
		}
		out = append(out, job.WorkObservation{Kind: job.UnitKind(kind), WallMS: wallMS, Dimension: job.WorkloadDimension(dimension), Amount: amount})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list preparation work observations: %w", err)
	}
	return out, nil
}
