package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"velox/go-master/internal/media/models"
	"velox/go-master/pkg/timeutil"
)

// ClaimNext picks the next eligible job (status='queued', lease expired)
// and atomically transitions it to 'running' under the supplied lease.
//
// The legacy implementation used an explicit *sql.Tx wrapping the
// SELECT + UPDATE, which doesn't translate to Postgres (where serializable
// transactions are much heavier and the lock dialect differs). The
// refactor uses the new Transition primitive with optimistic locking:
//   1. SELECT the next eligible row with the canonical column list.
//   2. Transition(queued → running, ExpectedRevision=row.Revision).
//   3. If RowsAffected == 0, another worker raced us → return nil
//      (caller polls again next tick).
//
// claimMu is preserved to serialise calls within the same process and
// avoid thundering-herd SELECT scans.
func (r *Repository) ClaimNext(ctx context.Context, workerID string, leaseTTL time.Duration, types []models.JobType) (*models.Job, error) {
	r.claimMu.Lock()
	defer r.claimMu.Unlock()

	candidate, err := r.findQueuedCandidate(ctx, time.Now(), types)
	if err != nil {
		return nil, err
	}
	if candidate == nil {
		return nil, nil
	}

	leaseExpiry := time.Now().Add(leaseTTL)
	now := time.Now()
	updates := map[string]any{
		"worker_id":   workerID,
		"lease_expiry": leaseExpiry,
	}
	if candidate.StartedAt == nil {
		updates["started_at"] = now
	}

	claimed, err := r.Transition(ctx, TransitionRequest{
		JobID:            candidate.ID,
		ExpectedRevision: candidate.Revision,
		ExpectedStatus:   models.StatusQueued,
		NewStatus:        models.StatusRunning,
		Updates:          updates,
	})
	if err != nil {
		// Optimistic-lock collision is expected under contention: another
		// worker beat us to the same job. Return nil so the caller polls
		// again next tick instead of logging noise.
		if errors.Is(err, ErrOptimisticLockFailed) {
			return nil, nil
		}
		return nil, fmt.Errorf("claim: transition: %w", err)
	}
	return claimed, nil
}

// findQueuedCandidate is the SELECT half of ClaimNext. It returns the
// single best candidate (highest priority, oldest) that is eligible,
// or (nil, nil) when no row matches. Errors beyond ErrNoRows are
// returned to the caller verbatim.
func (r *Repository) findQueuedCandidate(ctx context.Context, now time.Time, types []models.JobType) (*models.Job, error) {
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE status = 'queued' AND (lease_expiry IS NULL OR lease_expiry < ?)`
	args := []any{timeutil.FormatRFC3339(now)}

	if len(types) > 0 {
		placeholders := make([]string, len(types))
		for i, t := range types {
			placeholders[i] = "?"
			args = append(args, t)
		}
		query += ` AND type IN (` + strings.Join(placeholders, ",") + `)`
	}

	query += ` ORDER BY priority DESC, created_at ASC LIMIT 1`

	row := r.db.QueryRowContext(ctx, query, args...)
	job := &models.Job{}
	if err := scanJobColumns(row, job); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("findQueuedCandidate: scan: %w", err)
	}
	return job, nil
}

func (r *Repository) RenewLease(ctx context.Context, jobID string, workerID string, leaseTTL time.Duration) error {
	leaseExpiry := time.Now().Add(leaseTTL)
	query := `UPDATE jobs SET lease_expiry = ?, updated_at = ? WHERE id = ? AND worker_id = ? AND status = 'running'`
	_, err := r.db.ExecContext(ctx, query, timeutil.FormatRFC3339(leaseExpiry), timeutil.FormatRFC3339(time.Now()), jobID, workerID)
	return err
}

// RequeueExpiredLeases runs as a background sweeper (see scanner.go) and
// resets 'running' jobs whose lease has expired back to 'queued' so a
// future worker can claim them. The UPDATE is unguarded here because
// it's a sweep, not a transition: any running job with an expired lease
// is unconditionally reclaimable.
func (r *Repository) RequeueExpiredLeases(ctx context.Context) error {
	now := time.Now()
	query := `UPDATE jobs
		SET status = 'queued', worker_id = '', lease_expiry = NULL, updated_at = ?
		WHERE status = 'running' AND lease_expiry < ?`
	_, err := r.db.ExecContext(ctx, query, timeutil.FormatRFC3339(now), timeutil.FormatRFC3339(now))
	return err
}

func (r *Repository) MarkRunningJobsOlderThanFailed(ctx context.Context, cutoff time.Time, reason string) (int, error) {
	result, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = ?, error = ?, updated_at = ? WHERE status = ? AND updated_at < ?`,
		models.StatusFailed, reason, timeutil.FormatRFC3339(time.Now().UTC()),
		models.StatusRunning, timeutil.FormatRFC3339(cutoff))
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}
