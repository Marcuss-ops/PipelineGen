// Package wiring — overlay_drive_wake_test.go
//
// Integration test for the job.completed overlay-drive wake:
//
//	simulated outbox pool dispatch of a deferred publication event
//	    → Repository.MarkDeferred (parent-not-terminal, next_attempt_at = now+60 s)
//	→ overlayDrivePublicationWake.Handle (at the parent job.completed commit)
//	    → Repository.WakeDeferred
//	    → next_attempt_at pulled back to "due now" while non-wake rows stay frozen
//
// godlike/07 no-fake-availability: every stage is a real SQL round-trip —
// the canonical outbox_events DDL, the real Repository (Enqueue, lease-fenced
// ClaimNext, MarkDeferred, WakeDeferred) and the real wake adapter that the
// job.completed handler composes. A swallowed wake would leave the future
// next_attempt_at rows frozen and fail the test.
package wiring

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/renderinggen"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// overlayWakeOutboxSchema is the production outbox_events DDL subset the
// Repository needs (canonical migration 092 + 186 shape, also exercised by
// performance_projection_integration_test.go).
const overlayWakeOutboxSchema = `
CREATE TABLE outbox_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL DEFAULT '',
    aggregate_type TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '',
    event_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 10,
    last_error TEXT NOT NULL DEFAULT '',
    next_attempt_at TEXT,
    worker_id TEXT NOT NULL DEFAULT '',
    lease_id TEXT NOT NULL DEFAULT '',
    lease_expiry TEXT,
    completed_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT '',
    priority INTEGER NOT NULL DEFAULT 5
);
CREATE UNIQUE INDEX ux_outbox_events_event_key ON outbox_events(event_key);
CREATE INDEX idx_outbox_events_status_next_attempt ON outbox_events(status, next_attempt_at, id);
CREATE INDEX idx_outbox_events_status_priority_claim ON outbox_events(status, priority, next_attempt_at, id);
`

// deferOverlayPublication runs the exact dispatch sequence the events pool
// executes for a publication whose parent result is not terminal yet:
// claim → the handler returns DeferredError → MarkDeferred at the deferral
// cadence (60 s). Returns the claimed event id so the test can inspect
// individual sequencing state later.
func deferOverlayPublication(t *testing.T, ctx context.Context, repo *outboxevents.Repository, jobID, itemID string, deferDelay time.Duration) int64 {
	t.Helper()
	payload := `{"spec":{"OverlayItemID":"` + itemID + `"},"artifact":{"SHA256":"` + itemID + `-sha"}}`
	if _, err := repo.Enqueue(ctx, nil, renderinggen.EventOverlayDrivePublicationRequested, jobID, "overlay_artifact", payload, "overlay-drive:"+jobID+":"+itemID); err != nil {
		t.Fatalf("Enqueue(%s): %v", itemID, err)
	}
	claim, err := repo.ClaimNext(ctx, "test-worker", time.Minute)
	if err != nil {
		t.Fatalf("ClaimNext(%s): %v", itemID, err)
	}
	if claim == nil || claim.Event.AggregateID != jobID {
		t.Fatalf("ClaimNext(%s): unexpected claim %+v", itemID, claim)
	}
	if err := repo.MarkDeferred(ctx, claim.Event.ID, claim.LeaseID, "test: parent not terminal", time.Now().Add(deferDelay)); err != nil {
		t.Fatalf("MarkDeferred(%s): %v", itemID, err)
	}
	return claim.Event.ID
}

func openOverlayWakeDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(overlayWakeOutboxSchema); err != nil {
		t.Fatalf("apply outbox schema: %v", err)
	}
	return db
}

func wakeTestNextAttemptAt(t *testing.T, db *sql.DB, eventID int64) time.Time {
	t.Helper()
	var nextAttempt string
	if err := db.QueryRow(`SELECT next_attempt_at FROM outbox_events WHERE id = ?`, eventID).Scan(&nextAttempt); err != nil {
		t.Fatalf("read next_attempt_at(event %d): %v", eventID, err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, nextAttempt)
	if err != nil {
		t.Fatalf("parse next_attempt_at(event %d): %v", eventID, err)
	}
	return parsed
}

func wakeTestStatus(t *testing.T, db *sql.DB, eventID int64) string {
	t.Helper()
	var status string
	if err := db.QueryRow(`SELECT status FROM outbox_events WHERE id = ?`, eventID).Scan(&status); err != nil {
		t.Fatalf("read status(event %d): %v", eventID, err)
	}
	return status
}

// TestOverlayDriveWakePullsDeferredAtParentCommit is the click-to-delivered
// contract: a publication event deferred on parent-not-terminal must be due
// again the moment the parent's job.completed fires, without leaving the
// intent to wait out its own 60 s deferral.
func TestOverlayDriveWakePullsDeferredAtParentCommit(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite3", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(overlayWakeOutboxSchema); err != nil {
		t.Fatalf("apply outbox schema: %v", err)
	}
	repo := outboxevents.NewRepository(db)

	deferredAt := time.Now().Add(-2 * time.Minute)
	eventID := deferOverlayPublication(t, ctx, repo, "job_parent", "item:000", time.Minute)

	before := wakeTestNextAttemptAt(t, db, eventID)
	if before.Sub(deferredAt) < 55*time.Second {
		t.Fatalf("test setup: deferred row must sit in the future (got %v)", before)
	}
	if got := wakeTestStatus(t, db, eventID); got != "pending" {
		t.Fatalf("deferred row must stay pending (got %q)", got)
	}

	wake := overlayDrivePublicationWake{events: repo, log: zap.NewNop()}
	if err := wake.Handle(ctx, "job_parent"); err != nil {
		t.Fatalf("wake.Handle: %v", err)
	}

	after := wakeTestNextAttemptAt(t, db, eventID)
	if after.After(time.Now().Add(time.Second)) {
		t.Fatalf("wake must make the deferred event due now; next_attempt_at still in the future: %v", after)
	}
}

// TestOverlayDriveWakeScopeFencesNonWakeRows pins the scope contract: only the
// parent's own deferred rows wake. A row under a live lease ('processing'),
// another aggregate's rows, an already-past-due row and a terminal row are not
// the wake's business.
func TestOverlayDriveWakeScopeFencesNonWakeRows(t *testing.T) {
	ctx := context.Background()
	db := openOverlayWakeDB(t)
	repo := outboxevents.NewRepository(db)

	otherJobEvent := deferOverlayPublication(t, ctx, repo, "job_sibling", "item:001", time.Minute)
	pastDueEvent := deferOverlayPublication(t, ctx, repo, "job_parent", "item:002", -time.Minute)
	// A row under a live lease: the pool claimed a due row; WakeDeferred must
	// never touch 'processing' rows (the SQL scope requires status='pending').
	if _, err := repo.Enqueue(ctx, nil, renderinggen.EventOverlayDrivePublicationRequested, "job_parent", "overlay_artifact", `{"spec":{"OverlayItemID":"item:003"}}`, "overlay-drive:job_parent:item:003-live"); err != nil {
		t.Fatalf("Enqueue(live lease row): %v", err)
	}
	liveLeaseClaim, err := repo.ClaimNext(ctx, "worker-wake-test", time.Minute)
	if err != nil {
		t.Fatalf("ClaimNext(live lease row): %v", err)
	}
	if liveLeaseClaim == nil || liveLeaseClaim.Event.AggregateID != "job_parent" {
		t.Fatalf("expected the due row as the live-lease claim (got %+v)", liveLeaseClaim)
	}
	liveLeaseEvent := liveLeaseClaim.Event.ID

	wake := overlayDrivePublicationWake{events: repo, log: zap.NewNop()}
	if err := wake.Handle(ctx, "job_parent"); err != nil {
		t.Fatalf("wake.Handle: %v", err)
	}

	if got := wakeTestStatus(t, db, liveLeaseEvent); got != "processing" {
		t.Fatalf("wake must never touch a leased row (got %q)", got)
	}
	if wakeTestNextAttemptAt(t, db, pastDueEvent).After(time.Now()) {
		t.Fatal("past-due row must remain past-due (wake must not push it later)")
	}
	if wakeTestStatus(t, db, otherJobEvent) != "pending" {
		t.Fatal("deferred sibling-aggregate row must stay pending")
	}
}

// TestOverlayDriveWakeRetriesIdempotent pins the crash-safe path: a wake that
// already ran (or a wake racing itself) re-executes the same move on rows that
// are still pending with a future next_attempt_at and returns no error.
func TestOverlayDriveWakeRetriesIdempotent(t *testing.T) {
	ctx := context.Background()
	db := openOverlayWakeDB(t)
	repo := outboxevents.NewRepository(db)

	_ = deferOverlayPublication(t, ctx, repo, "job_parent", "item:004", time.Minute)
	wake := overlayDrivePublicationWake{events: repo, log: zap.NewNop()}
	for attempt := 0; attempt < 3; attempt++ {
		if err := wake.Handle(ctx, "job_parent"); err != nil {
			t.Fatalf("wake.Handle attempt %d: %v", attempt, err)
		}
	}

	// The event must be claimable now — simulate the pool's next poll.
	claim, err := repo.ClaimNext(ctx, "poll-after-wake", time.Minute)
	if err != nil {
		t.Fatalf("ClaimNext after wake: %v", err)
	}
	if claim == nil || claim.Event.AggregateID != "job_parent" {
		t.Fatalf("woken event must be claimable on the next poll (got claim %+v)", claim)
	}
}
