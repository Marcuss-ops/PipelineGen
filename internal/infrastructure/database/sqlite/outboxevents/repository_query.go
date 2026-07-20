// Read-only dashboard queries for the outbox Repository. These methods
// do NOT mutate state — they power the realtime.IndexHealth counters
// (pending_outbox, dead_letter) and any operator-facing dashboard
// surface that needs a snapshot of the current outbox state.
//
// Per godlike/06 SSOT one-canonical-owner-per-fact, the write
// methods (ClaimNext, MarkCompleted, etc.) live in repository_write.go;
// this file is the SOLE canonical owner of the read-only surface.
package outboxevents

import (
	"context"
	"fmt"
)

// CountByStatus returns the count of outbox events in a given status bucket.
// Used by realtime.IndexHealth for the pending_outbox and dead_letter counters.
func (r *Repository) CountByStatus(ctx context.Context, status string) (int64, error) {
	if r.db == nil {
		return 0, fmt.Errorf("outboxevents.Repository: db is nil")
	}
	if status == "" {
		return 0, fmt.Errorf("outboxevents.CountByStatus: status is required")
	}
	var n int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM outbox_events WHERE status = ?", status,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("outboxevents.CountByStatus(%q): %w", status, err)
	}
	return n, nil
}

// CountByEventTypeAndStatus returns the count of outbox events for a
// specific event_type in a specific status bucket.
func (r *Repository) CountByEventTypeAndStatus(ctx context.Context, eventType, status string) (int64, error) {
	if r.db == nil {
		return 0, fmt.Errorf("outboxevents.Repository: db is nil")
	}
	if eventType == "" {
		return 0, fmt.Errorf("outboxevents.CountByEventTypeAndStatus: eventType is required")
	}
	if status == "" {
		return 0, fmt.Errorf("outboxevents.CountByEventTypeAndStatus: status is required")
	}
	var n int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM outbox_events WHERE event_type = ? AND status = ?", eventType, status,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("outboxevents.CountByEventTypeAndStatus(%q,%q): %w", eventType, status, err)
	}
	return n, nil
}

// ListPending returns all pending/processing events for dashboard.
func (r *Repository) ListPending(ctx context.Context) ([]Event, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, event_type, aggregate_id, aggregate_type, payload_json,
		       status, attempt_count, max_attempts, last_error,
		       event_key, worker_id, lease_id, lease_expiry, completed_at,
		       created_at, updated_at
		FROM outbox_events
		WHERE status IN ('pending', 'processing')
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("outboxevents.ListPending: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		evt, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("outboxevents.ListPending scan: %w", err)
		}
		events = append(events, *evt)
	}
	return events, rows.Err()
}

// ListByStatus returns all events in a given status bucket, ordered
// by created_at DESC. Used by the operator dashboard to inspect
// failed / dead-letter / completed events.
func (r *Repository) ListByStatus(ctx context.Context, status string) ([]Event, error) {
	if r.db == nil {
		return nil, fmt.Errorf("outboxevents.Repository: db is nil")
	}
	if status == "" {
		return nil, fmt.Errorf("outboxevents.ListByStatus: status is required")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, event_type, aggregate_id, aggregate_type, payload_json,
		       status, attempt_count, max_attempts, last_error,
		       event_key, worker_id, lease_id, lease_expiry, completed_at,
		       created_at, updated_at
		FROM outbox_events
		WHERE status = ?
		ORDER BY created_at DESC
		LIMIT 100
	`, status)
	if err != nil {
		return nil, fmt.Errorf("outboxevents.ListByStatus(%q): %w", status, err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		evt, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("outboxevents.ListByStatus scan: %w", err)
		}
		events = append(events, *evt)
	}
	return events, rows.Err()
}
