// Package jobs — repository_events.go: event-timeline read + write helpers.
//
// Pure code-motion extraction from repository.go per PR-SPLIT-JOBS-REPO-RESIDUAL
// (wave LONG-FILES-DECOMPOSITION-V2-2026-07-06#PR-SPLIT-JOBS-REPO-RESIDUAL,
// July 2026). godlike/06 SSOT: this file is the canonical SOLE owner
// of the read-side surface on the `job_events` table (timeline of
// state-transition audit events for each job row).
//
// godlike/07 minimum-blast-radius: zero new exported symbols, zero
// signature changes. Per user spec "NO nuovi exported symbols" the
// planned `ScanEvent`/`InsertEvent` canonical helpers are NOT introduced
// in this PR — the existing inline `INSERT INTO job_events (...)`
// pattern in lifecycle_{complete,progress,aggregation}.go +
// repository_claims.go remains unchanged. Consolidation of the inline
// event-INSERT into a canonical typed helper is forward-pointer
// PR-EVENT-INSERT-HELPER (out of scope for pure code-motion).
//
// The rfc3339TimeScanner cross-file adapter used by ListEvents lives
// in repository_scanner.go (godlike/06 SSOT one-owner's-per-fact).
package jobs

import (
	"context"
	"encoding/json"
	"fmt"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// eventsColumns is the canonical SELECT projection for the job_events
// timeline table. Kept private (lowercase) per godlike/06 SSOT —
// only ListEvents in this file reads it; future timeline readers
// reuse it without exporting.
const eventsColumns = `id, job_id, type, message, data_json,
	strftime('%Y-%m-%dT%H:%M:%fZ', created_at) AS created_at`

// ListEvents returns all events for a given job, ordered by
// created_at ASC (chronological timeline). The strftime() canonical
// wrap on the SELECT ensures the Go SQLite driver (parseTime=false)
// surfaces the DATETIME column as RFC3339Nano TEXT — scanned via
// the rfc3339TimeScanner typed adapter (canonical SSOT in
// repository_scanner.go).
//
// RED-2 / JOBS-T01-001 regression-locked: any future change to this
// method's SELECT MUST keep the strftime wrap + the
// rfc3339TimeScanner scan target. The 3 RED-2 tests in
// repository_events_test.go cover round-trip stability, empty-result
// no-scan, and bad-format scan-error sentinel. Without the strftime
// wrap, the mattn driver fails to parse DATETIME → *time.Time.
func (r *SQLiteStore) ListEvents(ctx context.Context, jobID string) ([]job.Event, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+eventsColumns+` FROM job_events
		WHERE job_id = ?
		ORDER BY strftime('%Y-%m-%dT%H:%M:%fZ', created_at) ASC`, jobID)
	if err != nil {
		return nil, fmt.Errorf("listEvents: %w", err)
	}
	defer rows.Close()

	var events []job.Event
	for rows.Next() {
		var evt job.Event
		var dataJSON string
		createdAt := &evt.CreatedAt
		if err := rows.Scan(&evt.ID, &evt.JobID, &evt.Type, &evt.Message, &dataJSON, &rfc3339TimeScanner{t: createdAt}); err != nil {
			return nil, fmt.Errorf("listEvents: scan: %w", err)
		}
		if len(dataJSON) > 0 {
			json.Unmarshal([]byte(dataJSON), &evt.Data)
		}
		events = append(events, evt)
	}
	return events, nil
}
