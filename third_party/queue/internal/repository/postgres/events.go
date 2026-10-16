package postgres

import (
	"context"
	"database/sql"
	"encoding/json"

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
