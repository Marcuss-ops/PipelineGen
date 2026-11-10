// requeue.go owns the lease-expiry sweep: permanently failing expired jobs
// that exhausted their attempts and requeueing the rest, each with a closed
// attempt and an appended event.
package postgres

import (
	"context"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// RequeueExpired permanently fails expired jobs that exhausted their attempts
// and requeues the rest, recording the attempt outcome and an event for each.
//
// The sweep drains the expired-lease set in bounded, independently committed
// batches (RequeueBatchSize). Selecting every expired job in one transaction
// used to make a fleet-wide lease expiry a single operation that could not fit
// the repository's 15-second op timeout: it locked every row, issued several
// statements per job, and a timeout rolled the whole sweep back so the next
// attempt started from zero. Batching makes recovery advance monotonically, and
// each batch is its own transaction/deadline, so a later failure leaves the
// earlier batches durable. The returned count is everything that reached a
// terminal or requeued state, including batches completed before an error.
func (r *Repository) RequeueExpired(ctx context.Context, now time.Time) (int, error) {
	total := 0
	for {
		n, done, err := r.requeueExpiredBatch(ctx, now)
		total += n
		if err != nil {
			return total, err
		}
		if done {
			return total, nil
		}
	}
}

// requeueExpiredBatch recovers at most requeueBatchSize expired leases in one
// transaction. done reports that the expired set is exhausted (the batch was
// short), not that this sweep requeued nothing.
func (r *Repository) requeueExpiredBatch(ctx context.Context, now time.Time) (count int, done bool, err error) {
	ctx, cancel := r.opContext(ctx)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()

	limit := r.requeueBatchSize()
	rows, err := tx.QueryContext(ctx, `
		SELECT id, attempt_count, max_attempts
		FROM render_jobs
		WHERE state IN `+stateIn(model.StateRunning, model.StateFinalizing)+` AND lease_until IS NOT NULL AND lease_until < $1
		ORDER BY lease_until ASC
		LIMIT $2
		FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return 0, false, err
	}

	type expiredJob struct {
		id          string
		attempts    int
		maxAttempts int
	}
	var expired []expiredJob
	for rows.Next() {
		var e expiredJob
		if err := rows.Scan(&e.id, &e.attempts, &e.maxAttempts); err != nil {
			rows.Close()
			return 0, false, err
		}
		expired = append(expired, e)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, false, err
	}
	rows.Close()

	if len(expired) == 0 {
		// Nothing committed, but the expired set is drained for this sweep.
		return 0, true, nil
	}

	// One outcome event per expired job, flushed as a single multi-row INSERT:
	// the sweep runs on a timer and can see many expired leases in one pass, so
	// it pays one event statement for the whole batch instead of one per job.
	// The persisted rows are identical (same VALUES, same BIGSERIAL order =
	// the SELECT's order).
	var events []eventRow
	for _, e := range expired {
		attempt, err := runningAttemptID(ctx, tx, e.id)
		if err != nil {
			return 0, false, err
		}
		if err := finishAttempt(ctx, tx, attempt, attemptStatusLeaseExpired, "", "lease expired"); err != nil {
			return 0, false, err
		}

		if model.ShouldFailPermanently(e.attempts, e.maxAttempts) {
			if _, err := tx.ExecContext(ctx, `
				UPDATE render_jobs
				SET state = `+stateLiteral(model.StateFailed)+`, failed_at = now(), current_worker_id = NULL,
				    lease_until = NULL, error_message = 'lease expired, max attempts reached'
				WHERE id = $1`, e.id); err != nil {
				return 0, false, err
			}
			events = append(events, eventRow{
				eventType: eventJobFailed, jobID: e.id, attemptID: attempt,
				payload: map[string]any{"reason": "lease expired, max attempts reached"},
			})
		} else {
			if _, err := tx.ExecContext(ctx, `
				UPDATE render_jobs
				SET state = `+stateLiteral(model.StatePending)+`, current_worker_id = NULL, lease_until = NULL, queued_at = now()
				WHERE id = $1`, e.id); err != nil {
				return 0, false, err
			}
			events = append(events, eventRow{
				eventType: eventJobRequeued, jobID: e.id, attemptID: attempt,
				payload: map[string]any{"reason": "lease expired"},
			})
		}
	}

	if err := recordEvents(ctx, tx, events); err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	// A short batch proves the SELECT has no more expired rows; a full one may
	// or may not, so the caller runs one more (empty or non-empty) batch.
	return len(expired), len(expired) < limit, nil
}
