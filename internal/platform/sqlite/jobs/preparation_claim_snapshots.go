package jobs

import (
	"context"
	"fmt"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

func (r *SQLiteStore) SnapshotPreparationClaim(ctx context.Context, input job.PreparationClaimInput) (*job.PreparationClaimSnapshot, error) {
	if input.JobID == "" {
		return nil, fmt.Errorf("snapshot preparation claim requires job id")
	}
	attemptID := input.AttemptID
	if attemptID == "" {
		attemptID = input.JobID + "-" + timeutil.FormatRFC3339Nano(input.ClaimedAt)
	}
	now := input.ClaimedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var total, required, ready, running, missing int
	var estimatedSavedMS, speculativeWorkMS int64
	err := r.db.QueryRowContext(ctx, `WITH states AS (
		SELECT ju.required, COALESCE(u.state, '') AS state,
		COALESCE(u.expected_work_ms, 0) AS expected_work_ms
		FROM preparation_job_units ju
		LEFT JOIN preparation_units u ON u.fingerprint = ju.fingerprint
		WHERE ju.job_id = ?)
		SELECT COUNT(*), COALESCE(SUM(required),0),
		COALESCE(SUM(CASE WHEN state='READY' AND required=1 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN state='RUNNING' AND required=1 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN state NOT IN ('READY','RUNNING') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN state='READY' AND required=1 THEN expected_work_ms ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN state='RUNNING' AND required=1 THEN expected_work_ms ELSE 0 END),0)
		FROM states`, input.JobID).Scan(&total, &required, &ready, &running, &missing, &estimatedSavedMS, &speculativeWorkMS)
	if err != nil {
		return nil, fmt.Errorf("snapshot preparation claim: %w", err)
	}
	ratio := float64(0)
	if required > 0 {
		ratio = float64(ready) / float64(required)
	}
	metadata := input.Metadata
	if len(metadata) == 0 {
		metadata = []byte(`{}`)
	}
	snapshot := &job.PreparationClaimSnapshot{JobID: input.JobID, AttemptID: attemptID, JobRevision: input.JobRevision, ClaimedAt: now, TotalUnits: total, RequiredUnits: required, ReadyUnits: ready, RunningUnits: running, MissingUnits: missing, PreparedAtClaimRatio: ratio, EstimatedSavedMS: estimatedSavedMS, SpeculativeWorkMS: speculativeWorkMS, QueueWaitMS: input.QueueWaitMS, QueuePositionAtPlan: input.QueuePositionAtPlan, Metadata: metadata}
	_, err = r.db.ExecContext(ctx, `INSERT INTO preparation_claim_snapshots
		(job_id, attempt_id, job_revision, claimed_at, total_units, required_units,
		 ready_units, running_units, missing_units, prepared_ratio, estimated_saved_ms,
		 speculative_work_ms, queue_wait_ms, queue_position_at_plan, metadata_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(job_id, attempt_id) DO UPDATE SET job_revision=excluded.job_revision,
		claimed_at=excluded.claimed_at, total_units=excluded.total_units,
		required_units=excluded.required_units, ready_units=excluded.ready_units,
		running_units=excluded.running_units, missing_units=excluded.missing_units,
		prepared_ratio=excluded.prepared_ratio, estimated_saved_ms=excluded.estimated_saved_ms,
		speculative_work_ms=excluded.speculative_work_ms, queue_wait_ms=excluded.queue_wait_ms,
		queue_position_at_plan=excluded.queue_position_at_plan,
		metadata_json=excluded.metadata_json`, input.JobID, attemptID, input.JobRevision, timeutil.FormatRFC3339(now), total, required, ready, running, missing, ratio, estimatedSavedMS, speculativeWorkMS, input.QueueWaitMS, input.QueuePositionAtPlan, string(metadata))
	if err != nil {
		return nil, fmt.Errorf("persist preparation claim snapshot: %w", err)
	}
	return snapshot, nil
}
