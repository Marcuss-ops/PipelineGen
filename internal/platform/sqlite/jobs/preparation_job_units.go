package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// This sister file (AGENTS.md Pattern 5) owns the job→unit mapping surface
// of the preparation store: RegisterPreparationJobUnit /
// ListPreparationJobUnits / MarkPreparationJobUnitAdopted. The mapping table
// (preparation_job_units, migration 243) answers "which prepared-unit
// fingerprints does job N depend on, and which did it adopt?" — two jobs
// sharing a fingerprint share one row in preparation_units (cross-job
// singleflight); this table is the per-job projection.

// RegisterPreparationJobUnit records the job→unit dependency idempotently
// (INSERT OR IGNORE, PK = job_id + unit_id). Re-planning the same unit for
// the same job is a no-op; an existing row keeps its original planned_at.
// The fingerprint is the unit's content address: two jobs sharing a
// fingerprint end up referencing the same global prepared_units row.
func (r *SQLiteStore) RegisterPreparationJobUnit(ctx context.Context, input job.RegisterPreparationJobUnitInput) error {
	if input.JobID == "" || input.UnitID == "" || input.Fingerprint == "" {
		return fmt.Errorf("register preparation job unit requires job id, unit id, and fingerprint")
	}
	nowStr := timeutil.FormatRFC3339(time.Now().UTC())
	required := 0
	if input.Required {
		required = 1
	}
	var queueRank any
	if input.QueueRank != nil {
		queueRank = *input.QueueRank
	}
	_, err := r.db.ExecContext(ctx, `INSERT OR IGNORE INTO preparation_job_units
		(job_id, unit_id, fingerprint, required, adopted, queue_rank, planned_at)
		VALUES (?, ?, ?, ?, 0, ?, ?)`,
		input.JobID, input.UnitID, input.Fingerprint, required, queueRank, nowStr)
	if err != nil {
		return fmt.Errorf("register preparation job unit: %w", err)
	}
	return nil
}

// ListPreparationJobUnits returns the units registered for a job, ordered by
// queue rank (NULLs last) then unit ID, so the coordinator/adoption loop sees
// a deterministic topological-ish order.
func (r *SQLiteStore) ListPreparationJobUnits(ctx context.Context, jobID string) ([]job.PreparationJobUnit, error) {
	if jobID == "" {
		return nil, fmt.Errorf("list preparation job units requires job id")
	}
	rows, err := r.db.QueryContext(ctx, `SELECT job_id, unit_id, fingerprint, required, adopted,
		queue_rank, planned_at, adopted_at FROM preparation_job_units
		WHERE job_id = ? ORDER BY queue_rank IS NULL, queue_rank ASC, unit_id ASC`, jobID)
	if err != nil {
		return nil, fmt.Errorf("list preparation job units: %w", err)
	}
	defer rows.Close()

	out := []job.PreparationJobUnit{}
	for rows.Next() {
		var (
			row       job.PreparationJobUnit
			required  int
			adopted   int
			queueRank *int
			plannedAt string
			adoptedAt *string
		)
		if err := rows.Scan(&row.JobID, &row.UnitID, &row.Fingerprint, &required, &adopted,
			&queueRank, &plannedAt, &adoptedAt); err != nil {
			return nil, fmt.Errorf("scan preparation job unit: %w", err)
		}
		row.Required = required != 0
		row.Adopted = adopted != 0
		row.QueueRank = queueRank
		if t := parsePreparationTime(&plannedAt); t != nil {
			row.PlannedAt = *t
		}
		row.AdoptedAt = parsePreparationTime(adoptedAt)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list preparation job units: %w", err)
	}
	return out, nil
}

// MarkPreparationJobUnitAdopted sets adopted=1 + adopted_at for the job→unit
// mapping once the job reuses the prepared result at execution time.
// sql.ErrNoRows is returned when the mapping does not exist (adoption of an
// unplanned unit is not an error — the caller may have executed the work
// directly and simply has no mapping row to flip).
func (r *SQLiteStore) MarkPreparationJobUnitAdopted(ctx context.Context, jobID, unitID string) error {
	if jobID == "" || unitID == "" {
		return fmt.Errorf("mark preparation job unit adopted requires job id and unit id")
	}
	nowStr := timeutil.FormatRFC3339(time.Now().UTC())
	res, err := r.db.ExecContext(ctx, `UPDATE preparation_job_units
		SET adopted = 1, adopted_at = ? WHERE job_id = ? AND unit_id = ?`,
		nowStr, jobID, unitID)
	if err != nil {
		return fmt.Errorf("mark preparation job unit adopted: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
