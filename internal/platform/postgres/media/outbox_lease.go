// Package media — outbox_lease.go: the PostgreSQL outbox consumption-side
// claim/lease primitives (claim, complete, fail with exponential backoff).
//
// Extracted from outbox_worker.go (2026-09-12) to keep the worker under
// max_lines_per_file_strict=600 (godlike/08): the worker owns the drain
// loop and event handling; this file owns the lease-fenced SQL lifecycle.
// Lease fencing mirrors internal/platform/sqlite/outboxevents exactly
// (same status set, same lease columns, same exponential-backoff
// MarkFailed semantics) — one outbox fact family, two engine adapters.
package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"github.com/google/uuid"
)

// the event was re-assigned or is already terminal. Mirrors the SQLite
// outbox error so worker code is engine-agnostic.
var ErrLeaseLost = errors.New("outbox lease lost")

// OutboxEvent is the consumption projection of one outbox_events row
// (SQLite outboxevents.Event parity).
type OutboxEvent struct {
	ID          int64
	EventType   string
	AggregateID string
	// AggregateType mirrors the SQLite envelope ("asset").
	AggregateType string
	PayloadJSON   string
	Status        string
	AttemptCount  int
	MaxAttempts   int
	LastError     string
	EventKey      string
	Priority      int
	CreatedAt     string
	UpdatedAt     string
}

// OutboxClaim is one claimed event plus its lease identity.
type OutboxClaim struct {
	Event    OutboxEvent
	WorkerID string
	LeaseID  string
}

// OutboxHandler performs the work for one non-index event claimed from the
// PostgreSQL media outbox. The worker owns the lease lifecycle: a successful
// handler return is followed by MarkCompleted, while an error is retried or
// dead-lettered through the canonical lease-fenced path.
type OutboxHandler interface {
	Handle(ctx context.Context, claim *OutboxClaim) error
}

// OutboxStatusMetrics is the narrow observability port for the PostgreSQL
// outbox. The worker owns the SQL truth; the metrics implementation only
// projects the current status counts and never becomes a second store.
type OutboxStatusMetrics interface {
	ObserveOutboxStatus(eventType, status string, count int64)
	// ObserveOutboxBacklog projects the queue depth and the age of the
	// oldest unprocessed event so a stalled drain is visible without the
	// worker paying a COUNT(*) probe per claim.
	ObserveOutboxBacklog(eventType string, backlogCount int64, oldestEventAgeSeconds float64)
	// ObserveOutboxProcessed records one event that reached terminal success.
	// It is the numerator for the processing-rate panel: Prometheus derives
	// rate(media_outbox_processed_total[5m]) per event type, so a flat rate
	// against a rising backlog is the unambiguous "drain is stuck" signal.
	ObserveOutboxProcessed(eventType string)
}

// ClaimNext claims the oldest pending event atomically (CTE claim with
// row-level fencing — PostgreSQL UPDATE ... WHERE status='pending' is
// atomic under concurrent workers). Ordering: priority DESC,
// next_attempt_at ASC, id ASC (migration 186 parity).
func (r *Repository) ClaimNext(ctx context.Context, workerID string, leaseTTL time.Duration) (*OutboxClaim, error) {
	now := timeutil.FormatRFC3339(time.Now())
	leaseID := uuid.NewString()
	leaseExpiry := timeutil.FormatRFC3339(time.Now().Add(leaseTTL))

	var id int64
	err := r.db.QueryRowContext(ctx, `
		WITH candidate AS (
			SELECT id FROM outbox_events
			WHERE status = 'pending'
			  AND (next_attempt_at IS NULL OR next_attempt_at <= $1)
			ORDER BY priority DESC, next_attempt_at ASC, id ASC
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE outbox_events
		SET status = 'processing',
		    attempt_count = attempt_count + 1,
		    worker_id = $2, lease_id = $3, lease_expiry = $4,
		    updated_at = $1
		WHERE id = (SELECT id FROM candidate)
		  AND status = 'pending'
		RETURNING id
	`, now, workerID, leaseID, leaseExpiry).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("media outbox ClaimNext: %w", err)
	}

	var evt OutboxEvent
	err = r.db.QueryRowContext(ctx, `
		SELECT id, event_type, aggregate_id, aggregate_type, payload_json,
		       status, attempt_count, max_attempts, last_error, event_key,
		       priority, created_at, updated_at
		FROM outbox_events WHERE id = $1
	`, id).Scan(&evt.ID, &evt.EventType, &evt.AggregateID, &evt.AggregateType, &evt.PayloadJSON,
		&evt.Status, &evt.AttemptCount, &evt.MaxAttempts, &evt.LastError, &evt.EventKey,
		&evt.Priority, &evt.CreatedAt, &evt.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("media outbox ClaimNext refetch(%d): %w", id, err)
	}
	return &OutboxClaim{Event: evt, WorkerID: workerID, LeaseID: leaseID}, nil
}

// outboxDrainBatchSize bounds how many events one ClaimBatch takes. A batch
// amortises the search_text read (N assets, ONE query) without holding a
// lease over an unbounded set.
const outboxDrainBatchSize = 32

// ClaimBatch claims up to limit pending events in ONE statement under a
// single lease token. Ordering and fencing match ClaimNext (priority DESC,
// next_attempt_at ASC, id ASC; FOR UPDATE SKIP LOCKED so concurrent workers
// never contend). Returns nil when the outbox has no claimable work.
//
// Each returned event already carries its incremented attempt_count (the
// UPDATE ... RETURNING projection), so no per-event refetch is needed.
func (r *Repository) ClaimBatch(ctx context.Context, workerID string, leaseTTL time.Duration, limit int) ([]*OutboxClaim, error) {
	if limit < 1 {
		limit = 1
	}
	now := timeutil.FormatRFC3339(time.Now())
	leaseID := uuid.NewString()
	leaseExpiry := timeutil.FormatRFC3339(time.Now().Add(leaseTTL))

	rows, err := r.db.QueryContext(ctx, `
		WITH candidate AS (
			SELECT id FROM outbox_events
			WHERE status = 'pending'
			  AND (next_attempt_at IS NULL OR next_attempt_at <= $1)
			ORDER BY priority DESC, next_attempt_at ASC, id ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE outbox_events
		SET status = 'processing',
		    attempt_count = attempt_count + 1,
		    worker_id = $3, lease_id = $4, lease_expiry = $5,
		    updated_at = $1
		WHERE id IN (SELECT id FROM candidate)
		  AND status = 'pending'
		RETURNING id, event_type, aggregate_id, aggregate_type, payload_json,
		          status, attempt_count, max_attempts, last_error, event_key,
		          priority, created_at, updated_at
	`, now, limit, workerID, leaseID, leaseExpiry)
	if err != nil {
		return nil, fmt.Errorf("media outbox ClaimBatch: %w", err)
	}
	defer rows.Close()

	var claims []*OutboxClaim
	for rows.Next() {
		var evt OutboxEvent
		if err := rows.Scan(&evt.ID, &evt.EventType, &evt.AggregateID, &evt.AggregateType, &evt.PayloadJSON,
			&evt.Status, &evt.AttemptCount, &evt.MaxAttempts, &evt.LastError, &evt.EventKey,
			&evt.Priority, &evt.CreatedAt, &evt.UpdatedAt); err != nil {
			return nil, fmt.Errorf("media outbox ClaimBatch scan: %w", err)
		}
		claims = append(claims, &OutboxClaim{Event: evt, WorkerID: workerID, LeaseID: leaseID})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("media outbox ClaimBatch iterate: %w", err)
	}
	return claims, nil
}

// MarkCompleted completes a claimed event (lease-fenced).
func (r *Repository) MarkCompleted(ctx context.Context, eventID int64, leaseID string) error {
	now := timeutil.FormatRFC3339(time.Now())
	result, err := r.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET status = 'completed', completed_at = $1, updated_at = $1
		WHERE id = $2 AND status = 'processing' AND lease_id = $3
	`, now, eventID, leaseID)
	if err != nil {
		return fmt.Errorf("media outbox MarkCompleted(%d): %w", eventID, err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return fmt.Errorf("media outbox MarkCompleted(%d): %w", eventID, ErrLeaseLost)
	}
	return nil
}

// MarkFailed records a failed attempt. Attempts remaining → back to
// pending with exponential backoff; exhausted → dead_letter.
func (r *Repository) MarkFailed(ctx context.Context, eventID int64, leaseID, errMsg string, nextAttemptAt time.Time) error {
	var attemptCount, maxAttempts int
	if err := r.db.QueryRowContext(ctx,
		`SELECT attempt_count, max_attempts FROM outbox_events WHERE id = $1`, eventID,
	).Scan(&attemptCount, &maxAttempts); err != nil {
		return fmt.Errorf("media outbox MarkFailed read(%d): %w", eventID, err)
	}
	now := timeutil.FormatRFC3339(time.Now())

	if attemptCount >= maxAttempts {
		result, err := r.db.ExecContext(ctx, `
			UPDATE outbox_events
			SET status = 'dead_letter', last_error = $1, updated_at = $2,
			    worker_id = '', lease_id = '', lease_expiry = NULL
			WHERE id = $3 AND lease_id = $4 AND status = 'processing'
		`, errMsg, now, eventID, leaseID)
		if err != nil {
			return fmt.Errorf("media outbox MarkFailed dead_letter(%d): %w", eventID, err)
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return fmt.Errorf("media outbox MarkFailed dead_letter(%d): %w", eventID, ErrLeaseLost)
		}
		return nil
	}

	result, err := r.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET status = 'pending', last_error = $1, next_attempt_at = $2,
		    updated_at = $3, worker_id = '', lease_id = '', lease_expiry = NULL
		WHERE id = $4 AND lease_id = $5 AND status = 'processing'
	`, errMsg, timeutil.FormatRFC3339(nextAttemptAt), now, eventID, leaseID)
	if err != nil {
		return fmt.Errorf("media outbox MarkFailed retry(%d): %w", eventID, err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return fmt.Errorf("media outbox MarkFailed retry(%d): %w", eventID, ErrLeaseLost)
	}
	return nil
}
