package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/queue/client"
)

// Event types recorded in render_events. They are ALIASES of the canonical
// wire vocabulary (queue/client), not a second declaration: the ledger, the
// HTTP projections, the worker and the end-to-end verification scripts must
// all name the same events, and the historical private copy here forced every
// other carrier to re-type the strings.
const (
	eventJobCreated   = client.EventJobCreated
	eventJobClaimed   = client.EventJobClaimed
	eventLeaseRenewed = client.EventLeaseRenewed
	eventJobCompleted = client.EventJobCompleted
	eventJobFailed    = client.EventJobFailed
	eventJobRequeued  = client.EventJobRequeued
	eventJobRendered  = client.EventJobRendered
	eventJobCancelled = client.EventJobCancelled
)

// eventRow is one render_events row staged for insertion. Multi-event
// transactions (SubmitBatch, the lease-expiry sweep) collect rows and flush
// them with ONE multi-row INSERT instead of one statement per event.
type eventRow struct {
	eventType string
	jobID     string
	attemptID string
	workerID  string
	payload   any
}

// recordEvent appends an event to render_events. Empty attempt/worker IDs are
// stored as NULL; a nil payload is stored as an empty JSON object.
func recordEvent(ctx context.Context, tx *sql.Tx, eventType, jobID, attemptID, workerID string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if len(raw) == 0 || string(raw) == "null" {
		raw = []byte("{}")
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO render_events (job_id, attempt_id, worker_id, event_type, payload)
		VALUES ($1, $2, $3, $4, $5)`,
		jobID, nullIfEmpty(attemptID), nullIfEmpty(workerID), eventType, raw)
	return err
}

// recordEvents appends a batch of events with ONE multi-row INSERT. Ordering
// is part of the contract: render_events.id is BIGSERIAL, and PostgreSQL
// assigns sequence values to a single INSERT ... VALUES statement in VALUES
// order, so the persisted order equals the historical per-event insert order
// (queryEvents in the tests reads ORDER BY id). A single-row batch delegates
// to recordEvent so the one-event statement shape stays literally unchanged.
func recordEvents(ctx context.Context, tx *sql.Tx, rows []eventRow) error {
	if len(rows) == 0 {
		return nil
	}
	if len(rows) == 1 {
		r := rows[0]
		return recordEvent(ctx, tx, r.eventType, r.jobID, r.attemptID, r.workerID, r.payload)
	}
	query, args, err := buildEventsInsertSQL(rows)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, query, args...)
	return err
}

// buildEventsInsertSQL renders the multi-row INSERT and its arguments in row
// order. It is pure so the batch shape (placeholder numbering, argument
// order, payload encoding) is unit-testable without a database.
func buildEventsInsertSQL(rows []eventRow) (string, []any, error) {
	var values strings.Builder
	args := make([]any, 0, len(rows)*5)
	for i, row := range rows {
		raw, err := json.Marshal(row.payload)
		if err != nil {
			return "", nil, err
		}
		if len(raw) == 0 || string(raw) == "null" {
			raw = []byte("{}")
		}
		if i > 0 {
			values.WriteByte(',')
		}
		base := i * 5
		fmt.Fprintf(&values, "($%d, $%d, $%d, $%d, $%d)", base+1, base+2, base+3, base+4, base+5)
		args = append(args, row.jobID, nullIfEmpty(row.attemptID), nullIfEmpty(row.workerID), row.eventType, raw)
	}
	query := `
		INSERT INTO render_events (job_id, attempt_id, worker_id, event_type, payload)
		VALUES ` + values.String()
	return query, args, nil
}
