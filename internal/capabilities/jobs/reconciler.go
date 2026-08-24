// Package finalizer — reconciler.go (Spina Dorsale, FASE 2c, July 2026).
//
// PublicationIntentReconciler scans publication_intents for rows stuck
// in PUBLISHED state (upload succeeded, but the SQLite transaction that
// should have committed the asset_location never completed). Each such
// row represents a potential orphan: the file exists on the remote
// provider (Drive, S3), but no asset_location row links it back to
// the asset catalog.
//
// Recovery strategy (Piano d'Azione § 4.7):
//
//  1. Check if the job is still active (RUNNING / FINALIZING):
//     → leave the intent as PUBLISHED (the worker is still retrying).
//
//  2. Check if the job is SUCCEEDED:
//     → the commit happened, the intent just wasn't updated → COMMITTED.
//
//  3. Check if a corresponding asset_locations row exists:
//     → the commit happened, the intent just wasn't updated → COMMITTED.
//
//  4. Otherwise (job FAILED / CANCELLED / missing):
//     → the job will never retry, and no asset_location exists.
//     → the remote file is a true orphan → ORPHANED.
//
// Once ORPHANED, a separate cleanup cycle (future FASE) can:
//
//	ORPHANED → CLEANUP_PENDING → CLEANED (file deleted from remote).
//
// The reconciler is designed to run as a background ticker (e.g. every 5
// minutes). It is safe to run concurrently with normal operations because:
//   - The scan uses a time-threshold (rows older than N minutes) to avoid
//     racing with in-flight commits.
//   - The state transition is idempotent (re-marking PUBLISHED→ORPHANED is
//     harmless).
//   - The asset_locations check is a simple SELECT — no locking needed.
package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// PublicationIntentReconciler scans and recovers orphan publication intents.
type PublicationIntentReconciler struct {
	db  *sql.DB
	log *zap.Logger
}

// NewReconciler creates a new PublicationIntentReconciler.
func NewReconciler(db *sql.DB, log *zap.Logger) *PublicationIntentReconciler {
	if log == nil {
		log = zap.NewNop()
	}
	return &PublicationIntentReconciler{db: db, log: log}
}

// ReconcileOrphanResult reports the outcome of one reconciliation sweep.
type ReconcileOrphanResult struct {
	Scanned         int // total PUBLISHED rows examined
	MarkedOrphan    int // rows where no asset_location + job terminal/missing → ORPHANED
	MarkedCommitted int // rows where asset_location or job SUCCEEDED → COMMITTED
	SkippedActive   int // rows where job is still RUNNING/FINALIZING → left as PUBLISHED
	Errors          int // rows where the update failed
}

// intentRow is the scanned result of a PUBLISHED publication_intents row.
type intentRow struct {
	id             int64
	jobID          string
	artifactID     string
	remoteFileID   string
	idempotencyKey string
}

// ReconcileOrphans scans publication_intents for rows stuck in PUBLISHED
// state older than `olderThan` and recovers them.
//
// For each PUBLISHED row, the recovery logic (per the Piano §4.7) is:
//
//  1. If the job is still active (RUNNING / FINALIZING) → skip (the worker
//     may still retry the finalization).
//  2. If the job is SUCCEEDED → mark COMMITTED (commit happened, intent
//     just wasn't updated).
//  3. If an asset_locations row exists (by artifact_id or remote_file_id)
//     → mark COMMITTED (commit happened).
//  4. Otherwise (job FAILED / CANCELLED / missing and no location)
//     → mark ORPHANED (true orphan: file on Drive, no local record,
//     job will never retry).
//
// The olderThan threshold prevents racing with in-flight commits.
func (r *PublicationIntentReconciler) ReconcileOrphans(ctx context.Context, olderThan time.Duration) (*ReconcileOrphanResult, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)

	rows, err := r.db.QueryContext(ctx,
		`SELECT id, job_id, artifact_id, remote_file_id, idempotency_key
		 FROM publication_intents
		 WHERE state = 'PUBLISHED' AND updated_at < ?
		 ORDER BY updated_at ASC
		 LIMIT 500`,
		cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("reconciler: scan published intents: %w", err)
	}
	defer rows.Close()

	result := &ReconcileOrphanResult{}

	var intents []intentRow
	for rows.Next() {
		var ir intentRow
		if err := rows.Scan(&ir.id, &ir.jobID, &ir.artifactID, &ir.remoteFileID, &ir.idempotencyKey); err != nil {
			r.log.Warn("reconciler: scan row failed", zap.Error(err))
			result.Errors++
			continue
		}
		intents = append(intents, ir)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reconciler: rows iteration: %w", err)
	}

	result.Scanned = len(intents)

	for _, ir := range intents {
		newState, skip, err := r.determineRecovery(ctx, &ir)
		if err != nil {
			r.log.Warn("reconciler: determine recovery failed",
				zap.Int64("intent_id", ir.id),
				zap.String("artifact_id", ir.artifactID),
				zap.Error(err),
			)
			result.Errors++
			continue
		}
		if skip {
			result.SkippedActive++
			continue
		}

		if err := r.transitionState(ctx, ir.id, newState); err != nil {
			r.log.Warn("reconciler: state transition failed",
				zap.Int64("intent_id", ir.id),
				zap.String("new_state", newState),
				zap.Error(err),
			)
			result.Errors++
			continue
		}

		if newState == "COMMITTED" {
			result.MarkedCommitted++
			r.log.Debug("reconciler: marked COMMITTED",
				zap.Int64("intent_id", ir.id),
				zap.String("job_id", ir.jobID),
			)
		} else {
			result.MarkedOrphan++
			r.log.Warn("reconciler: marked ORPHANED (remote file may be orphan)",
				zap.Int64("intent_id", ir.id),
				zap.String("artifact_id", ir.artifactID),
				zap.String("remote_file_id", ir.remoteFileID),
				zap.String("job_id", ir.jobID),
			)
		}
	}

	return result, nil
}

// determineRecovery decides what to do with a PUBLISHED intent:
//
//   - ("COMMITTED", false, nil) → mark COMMITTED
//   - ("ORPHANED", false, nil)  → mark ORPHANED
//   - ("", true, nil)           → skip (job still active, leave as PUBLISHED)
func (r *PublicationIntentReconciler) determineRecovery(ctx context.Context, ir *intentRow) (newState string, skip bool, err error) {
	// Step 1: check job status.
	if ir.jobID != "" {
		jobStatus, hasJob, jobErr := r.getJobStatus(ctx, ir.jobID)
		if jobErr != nil {
			return "", false, fmt.Errorf("get job status: %w", jobErr)
		}
		if hasJob {
			switch jobStatus {
			case "RUNNING", "FINALIZING", "LEASED":
				// Job is still active — the worker may retry the
				// finalization. Leave the intent as PUBLISHED.
				r.log.Debug("reconciler: skipping active job",
					zap.String("job_id", ir.jobID),
					zap.String("job_status", jobStatus),
				)
				return "", true, nil
			case "SUCCEEDED":
				// Job already succeeded — the commit must have
				// happened (or the job was completed without this
				// artifact's finalization, which is a different
				// kind of inconsistency).
				return "COMMITTED", false, nil
			}
			// FAILED, CANCELLED, RETRY_WAIT, QUEUED: the job will
			// never retry this specific attempt. Fall through to
			// asset_locations check.
		}
	}

	// Step 2: check if asset_locations row exists.
	hasLocation, locErr := r.hasAssetLocation(ctx, ir.artifactID, ir.remoteFileID)
	if locErr != nil {
		return "", false, fmt.Errorf("check asset_location: %w", locErr)
	}
	if hasLocation {
		return "COMMITTED", false, nil
	}

	// Step 3: no asset_location, job is terminal or missing → orphan.
	return "ORPHANED", false, nil
}

// getJobStatus returns the status of a job by ID.
func (r *PublicationIntentReconciler) getJobStatus(ctx context.Context, jobID string) (status string, found bool, err error) {
	err = r.db.QueryRowContext(ctx,
		`SELECT status FROM jobs WHERE id = ?`, jobID,
	).Scan(&status)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return status, true, nil
}

// hasAssetLocation checks whether an asset_locations row exists for the
// given artifact_id or remote_file_id.
func (r *PublicationIntentReconciler) hasAssetLocation(ctx context.Context, artifactID, remoteFileID string) (bool, error) {
	var count int

	// Check by artifact_id first (canonical lookup).
	if artifactID != "" {
		err := r.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM asset_locations WHERE asset_id = ?`,
			artifactID,
		).Scan(&count)
		if err != nil {
			return false, fmt.Errorf("query asset_locations by asset_id: %w", err)
		}
		if count > 0 {
			return true, nil
		}
	}

	// Fallback: check by external_id (remote_file_id on Drive).
	if remoteFileID != "" {
		err := r.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM asset_locations WHERE external_id = ?`,
			remoteFileID,
		).Scan(&count)
		if err != nil {
			return false, fmt.Errorf("query asset_locations by external_id: %w", err)
		}
		return count > 0, nil
	}

	return false, nil
}

// transitionState updates the state of a publication_intents row.
func (r *PublicationIntentReconciler) transitionState(ctx context.Context, intentID int64, newState string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := r.db.ExecContext(ctx,
		`UPDATE publication_intents
		 SET state = ?, updated_at = ?
		 WHERE id = ? AND state = 'PUBLISHED'`,
		newState, now, intentID,
	)
	if err != nil {
		return fmt.Errorf("update state: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		// Another reconciler already transitioned this row — idempotent.
		return nil
	}
	return nil
}

// ListOrphans returns all ORPHANED publication intents for operator
// inspection. Useful for dashboards and manual cleanup.
func (r *PublicationIntentReconciler) ListOrphans(ctx context.Context) ([]OrphanInfo, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, job_id, artifact_id, remote_file_id, provider, state,
		        idempotency_key, last_error, created_at, updated_at
		 FROM publication_intents
		 WHERE state = 'ORPHANED'
		 ORDER BY updated_at DESC
		 LIMIT 100`,
	)
	if err != nil {
		return nil, fmt.Errorf("reconciler: list orphans: %w", err)
	}
	defer rows.Close()

	var out []OrphanInfo
	for rows.Next() {
		var o OrphanInfo
		if err := rows.Scan(
			&o.ID, &o.JobID, &o.ArtifactID, &o.RemoteFileID,
			&o.Provider, &o.State, &o.IdempotencyKey,
			&o.LastError, &o.CreatedAt, &o.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("reconciler: scan orphan: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// OrphanInfo is the operator-facing view of an orphaned publication intent.
type OrphanInfo struct {
	ID             int64  `json:"id"`
	JobID          string `json:"job_id"`
	ArtifactID     string `json:"artifact_id"`
	RemoteFileID   string `json:"remote_file_id"`
	Provider       string `json:"provider"`
	State          string `json:"state"`
	IdempotencyKey string `json:"idempotency_key"`
	LastError      string `json:"last_error"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}
