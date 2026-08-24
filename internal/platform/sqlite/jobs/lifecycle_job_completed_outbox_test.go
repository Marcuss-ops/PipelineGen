package jobs

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// TestJobLifecycle_CompleteEmitsJobCompletedOutboxEvent pins the durable
// derived-projection trigger: a SUCCEEDED flip emits exactly one
// job.completed outbox event with aggregate_id = job id, atomically with the
// status flip. The performance-projection handler consumes this event.
func TestJobLifecycle_CompleteEmitsJobCompletedOutboxEvent(t *testing.T) {
	store, db := setupLifecycleTestDB(t)
	ctx := context.Background()
	const jobID = "outbox-complete"

	p1bSeedRunningJob(t, db, jobID, "script.generate", 3, "worker-A", "lease-1",
		time.Now().Add(5*time.Minute), 0)
	if err := store.Complete(ctx, jobID, "worker-A", "lease-1", 1, []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM outbox_events WHERE event_type = ? AND aggregate_id = ?`,
		outboxevents.EventJobCompleted, jobID,
	).Scan(&count); err != nil {
		t.Fatalf("count job.completed outbox event: %v", err)
	}
	if count != 1 {
		t.Fatalf("job.completed outbox events = %d, want 1", count)
	}
	assertJobCompletedEventKey(t, db, jobID)
}

// TestJobLifecycle_FailEmitsJobCompletedOutboxEvent pins the same trigger on
// the FAILED path: derived projections must also rebuild from failed runs.
func TestJobLifecycle_FailEmitsJobCompletedOutboxEvent(t *testing.T) {
	store, db := setupLifecycleTestDB(t)
	ctx := context.Background()
	const jobID = "outbox-fail"

	p1bSeedRunningJob(t, db, jobID, "script.generate", 3, "worker-A", "lease-1",
		time.Now().Add(5*time.Minute), 0)
	if err := store.Fail(ctx, jobID, "worker-A", "lease-1", 1, "boom"); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM outbox_events WHERE event_type = ? AND aggregate_id = ?`,
		outboxevents.EventJobCompleted, jobID,
	).Scan(&count); err != nil {
		t.Fatalf("count job.completed outbox event: %v", err)
	}
	if count != 1 {
		t.Fatalf("job.completed outbox events = %d, want 1", count)
	}
	assertJobCompletedEventKey(t, db, jobID)
}

// assertJobCompletedEventKey pins the canonical cross-path dedup key: every
// producer (SQLiteStore.Complete/Fail, JobFinalizer, completion services)
// must emit `job.completed:<jobID>` so a retry or cross-path re-completion
// collapses to one outbox row via ON CONFLICT(event_key) DO NOTHING.
func assertJobCompletedEventKey(t *testing.T, db *sql.DB, jobID string) {
	t.Helper()
	var eventKey string
	if err := db.QueryRow(
		`SELECT event_key FROM outbox_events WHERE event_type = ? AND aggregate_id = ?`,
		outboxevents.EventJobCompleted, jobID,
	).Scan(&eventKey); err != nil {
		t.Fatalf("read job.completed event_key: %v", err)
	}
	if want := outboxevents.JobCompletedEventKey(jobID); eventKey != want {
		t.Fatalf("job.completed event_key = %q, want %q (canonical cross-path dedup key)", eventKey, want)
	}
}
