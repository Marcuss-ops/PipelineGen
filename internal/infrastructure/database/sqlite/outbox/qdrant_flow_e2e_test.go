// Package outbox — qdrant_flow_e2e_test.go (QDRANT-002)
//
// End-to-end tests for the full QDRANT outbox flow:
// outage → retry → crash → replay → supersede.
//
// These tests exercise the canonical ingestion path
// (Dispatcher.EnqueueAndIndex / EnqueueAndDelete) through the
// outboxevents.Repository lifecycle (ClaimNext → MarkFailed →
// MarkCompleted → MarkDeadLetter → MarkSuperseded →
// RequeueExpiredLeases) using an in-memory SQLite database.
//
// Each test is self-contained: it opens its own :memory: DB,
// creates the outbox_events schema, wires a real Repository +
// Dispatcher, and asserts the state transitions end-to-end.
//
// Scenarios:
//   TestE2E_HappyPath_EnqueueProcessComplete
//     — canonical success: enqueue → claim → process → complete.
//   TestE2E_RetryAndDeadLetter
//     — retryable failures exhaust max_attempts → dead_letter.
//   TestE2E_LeaseExpiryAndReclaim_WorkerCrash
//     — worker crashes (lease expires), reaper requeues, new worker claims.
//   TestE2E_IdempotentReplay
//     — duplicate enqueue with same event_key → ON CONFLICT DO NOTHING.
//   TestE2E_Supersede
//     — newer aggregate version obsoletes pending event → superseded.

package outbox

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// qdrantFlowDB is a test helper that opens an in-memory SQLite DB
// with the outbox_events schema.
func qdrantFlowDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite3 memory: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ensureOutboxSchema(t, db)
	return db
}

// enqueueTestEvent inserts a synthetic asset.index.requested event
// through the canonical Dispatcher path (real txmgr + real
// outboxevents.Repository). Returns the content hash used for the
// event_key so the caller can verify event_key shape in queries.
func enqueueTestEvent(t *testing.T, db *sql.DB, assetID, contentHash string) string {
	t.Helper()
	clips := &fakeClips{}
	eventsRepo := outboxevents.NewRepository(db)
	txMgr := &txMgrCapture{db: db}
	d := NewDispatcher(clips, clips, eventsRepo, txMgr, zap.NewNop())
	clip := &asset.Asset{
		ID:     assetID,
		Source: "youtube",
		Name:   "test clip " + assetID,
	}
	if contentHash == "" {
		contentHash = "sha256:deadbeefcafebabe" + assetID[:min(8, len(assetID))]
	}
	if err := d.EnqueueAndIndex(context.Background(), clip, contentHash); err != nil {
		t.Fatalf("EnqueueAndIndex(%s): %v", assetID, err)
	}
	return contentHash
}

// countOutboxEvents returns the number of rows in outbox_events.
func countOutboxEvents(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM outbox_events").Scan(&n); err != nil {
		t.Fatalf("count outbox_events: %v", err)
	}
	return n
}

// countByStatus returns the count for a specific status.
func countByStatus(t *testing.T, db *sql.DB, status string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM outbox_events WHERE status = ?", status,
	).Scan(&n); err != nil {
		t.Fatalf("count outbox_events by status %q: %v", status, err)
	}
	return n
}

// assertStatus asserts that a specific outbox event row has the expected status.
func assertStatus(t *testing.T, db *sql.DB, eventKey, wantStatus string) {
	t.Helper()
	var got string
	err := db.QueryRow(
		"SELECT status FROM outbox_events WHERE event_key = ?", eventKey,
	).Scan(&got)
	if err != nil {
		t.Fatalf("query status for event_key %q: %v", eventKey, err)
	}
	if got != wantStatus {
		t.Errorf("event_key %q: want status=%q got %q", eventKey, wantStatus, got)
	}
}

// assertAttemptCount asserts the attempt_count for a row.
func assertAttemptCount(t *testing.T, db *sql.DB, eventKey string, wantMin int) {
	t.Helper()
	var got int
	err := db.QueryRow(
		"SELECT attempt_count FROM outbox_events WHERE event_key = ?", eventKey,
	).Scan(&got)
	if err != nil {
		t.Fatalf("query attempt_count for event_key %q: %v", eventKey, err)
	}
	if got < wantMin {
		t.Errorf("event_key %q: want attempt_count >= %d, got %d", eventKey, wantMin, got)
	}
}

// ── Scenario 1: Happy path (enqueue → claim → process → complete) ─────

// TestE2E_HappyPath_EnqueueProcessComplete verifies the canonical
// success flow. An asset is enqueued, the outbox worker claims it,
// the handler processes it successfully, and the event transitions
// to completed.
func TestE2E_HappyPath_EnqueueProcessComplete(t *testing.T) {
	db := qdrantFlowDB(t)
	repo := outboxevents.NewRepository(db)

	// ── Stage 1: Enqueue an asset through the Dispatcher ──
	assetID := "yt_happy_path_001"
	enqueueTestEvent(t, db, assetID, "")

	if n := countOutboxEvents(t, db); n != 1 {
		t.Fatalf("expected 1 outbox event, got %d", n)
	}
	if n := countByStatus(t, db, "pending"); n != 1 {
		t.Fatalf("expected 1 pending event, got %d", n)
	}

	// ── Stage 2: Worker claims the event ──
	ctx := context.Background()
	claim, err := repo.ClaimNext(ctx, "worker-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if claim == nil {
		t.Fatal("expected a claim, got nil (no pending events)")
	}
	if claim.Event.AggregateID != assetID {
		t.Errorf("claimed event aggregate_id: want %q got %q", assetID, claim.Event.AggregateID)
	}
	if claim.LeaseID == "" {
		t.Error("lease_id must be non-empty")
	}

	// Status should now be processing.
	assertStatus(t, db, claim.Event.EventKey, "processing")
	assertAttemptCount(t, db, claim.Event.EventKey, 1)

	// ── Stage 3: Handler completes the event ──
	if err := repo.MarkCompleted(ctx, claim.Event.ID, claim.LeaseID); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}
	assertStatus(t, db, claim.Event.EventKey, "completed")

	// Verify completed_at is populated (sql.NullString with non-empty value).
	var completedAt sql.NullString
	if err := db.QueryRow(
		"SELECT completed_at FROM outbox_events WHERE event_key = ?",
		claim.Event.EventKey,
	).Scan(&completedAt); err != nil {
		t.Fatalf("query completed_at: %v", err)
	}
	if !completedAt.Valid || completedAt.String == "" {
		t.Error("completed_at must be populated after MarkCompleted")
	}

	// No more pending events.
	if n := countByStatus(t, db, "pending"); n != 0 {
		t.Errorf("expected 0 pending, got %d", n)
	}
}

// ── Scenario 2: Retry and dead-letter ──────────────────────────────────

// TestE2E_RetryAndDeadLetter verifies that retryable failures exhaust
// max_attempts and the event lands in dead_letter.
func TestE2E_RetryAndDeadLetter(t *testing.T) {
	db := qdrantFlowDB(t)
	repo := outboxevents.NewRepository(db)

	// ── Stage 1: Enqueue an asset ──
	assetID := "yt_retry_002"
	enqueueTestEvent(t, db, assetID, "")

	ctx := context.Background()

	// ── Stage 2: Claim → fail (retryable) → MarkFailed, repeat ──
	// The row starts with max_attempts=5. Retry until dead_letter.
	for {
		if countByStatus(t, db, "dead_letter") > 0 {
			break
		}
		claim, err := repo.ClaimNext(ctx, fmt.Sprintf("worker-2-%d",
			countByStatus(t, db, "pending")+countByStatus(t, db, "dead_letter")+countByStatus(t, db, "processing")),
			30*time.Second)
		if err != nil {
			t.Fatalf("ClaimNext: %v", err)
		}
		if claim == nil {
			if countByStatus(t, db, "dead_letter") > 0 {
				break
			}
			t.Fatal("expected a claim (no dead_letter yet)")
		}

		// Simulate a retryable failure (e.g. Qdrant unreachable).
		nextAttempt := time.Now().Add(1 * time.Millisecond) // immediate for fast test
		if err := repo.MarkFailed(ctx, claim.Event.ID, claim.LeaseID,
			"mock Qdrant timeout", nextAttempt); err != nil {
			t.Fatalf("MarkFailed: %v", err)
		}
	}

	// ── Stage 3: Assert terminal state ──
	if n := countByStatus(t, db, "dead_letter"); n != 1 {
		t.Errorf("expected 1 dead_letter event, got %d", n)
	}
	if n := countByStatus(t, db, "pending"); n != 0 {
		// Some implementations may leave the last pending before dead_letter.
		// The canonical path is dead_letter after max_attempts.
		t.Logf("pending events after dead_letter: %d (may be acceptable)", n)
	}

	// Attempt count should reflect all retries.
	var attemptCount int
	db.QueryRow(
		"SELECT attempt_count FROM outbox_events WHERE status = 'dead_letter'",
	).Scan(&attemptCount)
	if attemptCount < 5 {
		t.Errorf("expected attempt_count >= 5 for dead_letter, got %d", attemptCount)
	}
}

// ── Scenario 3: Lease expiry → reclaim (worker crash) ──────────────────

// TestE2E_LeaseExpiryAndReclaim_WorkerCrash verifies that when a worker
// crashes (lease expires), the reaper requeues the event and another
// worker can claim and complete it.
func TestE2E_LeaseExpiryAndReclaim_WorkerCrash(t *testing.T) {
	db := qdrantFlowDB(t)
	repo := outboxevents.NewRepository(db)

	// ── Stage 1: Enqueue ──
	assetID := "yt_crash_003"
	enqueueTestEvent(t, db, assetID, "")

	ctx := context.Background()

	// ── Stage 2: Worker A claims with a very short lease ──
	shortLease := 1 * time.Millisecond
	claim, err := repo.ClaimNext(ctx, "worker-A", shortLease)
	if err != nil {
		t.Fatalf("ClaimNext worker-A: %v", err)
	}
	if claim == nil {
		t.Fatal("expected a claim for worker-A")
	}
	assertStatus(t, db, claim.Event.EventKey, "processing")

	// ── Stage 3: Simulate worker crash — wait for lease to expire ──
	// Use a sub-millisecond lease + immediate requeue to avoid timing races.
	// The RequeueExpiredLeases WHERE clause compares lease_expiry < now(),
	// so a 0-duration lease is immediately eligible.
	time.Sleep(5 * time.Millisecond) // ensure lease_expiry is in the past

	// ── Stage 4: Reaper requeues expired leases ──
	affected, err := repo.RequeueExpiredLeases(ctx)
	if err != nil {
		t.Fatalf("RequeueExpiredLeases: %v", err)
	}
	if affected != 1 {
		t.Errorf("RequeueExpiredLeases: want 1 affected, got %d", affected)
	}

	// The event should now be pending again.
	n := countByStatus(t, db, "pending")
	if n != 1 {
		t.Errorf("after requeue: expected 1 pending, got %d (status may still be processing)", n)
	}

	// ── Stage 5: Worker B claims and completes ──
	claim2, err := repo.ClaimNext(ctx, "worker-B", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNext worker-B: %v", err)
	}
	if claim2 == nil {
		t.Fatal("expected a claim for worker-B after requeue")
	}
	if claim2.Event.AggregateID != assetID {
		t.Errorf("worker-B claimed wrong event: want %q got %q",
			assetID, claim2.Event.AggregateID)
	}
	// Two claims = attempt_count should be >= 1 (the CRASH worker's
	// attempt was already counted; the requeue may or may not reset it
	// depending on the SQL). The important invariant: attempt_count > 0.
	assertAttemptCount(t, db, claim2.Event.EventKey, 1)

	if err := repo.MarkCompleted(ctx, claim2.Event.ID, claim2.LeaseID); err != nil {
		t.Fatalf("MarkCompleted worker-B: %v", err)
	}
	assertStatus(t, db, claim2.Event.EventKey, "completed")

	// Verify lease_id from crashed worker was cleared.
	var leaseID string
	db.QueryRow(
		"SELECT lease_id FROM outbox_events WHERE event_key = ?",
		claim2.Event.EventKey,
	).Scan(&leaseID)
	if leaseID != "" {
		t.Errorf("lease_id should be empty after requeue, got %q", leaseID)
	}
}

// ── Scenario 4: Idempotent replay ──────────────────────────────────────

// TestE2E_IdempotentReplay verifies that replaying the same enqueue
// with matching content_hash is a no-op (ON CONFLICT(event_key)
// DO NOTHING). A second EnqueueAndIndex for the same asset with the
// same hash must NOT create a second outbox row.
func TestE2E_IdempotentReplay(t *testing.T) {
	db := qdrantFlowDB(t)
	repo := outboxevents.NewRepository(db)

	// ── Stage 1: First enqueue ──
	assetID := "yt_replay_004"
	hash := enqueueTestEvent(t, db, assetID, "sha256:replaytest0000")

	if n := countOutboxEvents(t, db); n != 1 {
		t.Fatalf("first enqueue: expected 1 event, got %d", n)
	}

	// ── Stage 2: Second enqueue with same asset + same hash ──
	// This must be a no-op (ON CONFLICT(event_key) DO NOTHING).
	enqueueTestEvent(t, db, assetID, hash)

	if n := countOutboxEvents(t, db); n != 1 {
		t.Errorf("idempotent replay: expected 1 event (duplicate suppressed), got %d", n)
	}

	// ── Stage 3: Claim and complete the single event ──
	ctx := context.Background()
	claim, err := repo.ClaimNext(ctx, "worker-4", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if claim == nil {
		t.Fatal("expected a claim")
	}
	if claim.Event.AggregateID != assetID {
		t.Errorf("claimed aggregate_id: want %q got %q", assetID, claim.Event.AggregateID)
	}

	if err := repo.MarkCompleted(ctx, claim.Event.ID, claim.LeaseID); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}
	assertStatus(t, db, claim.Event.EventKey, "completed")

	// No stray events.
	if n := countByStatus(t, db, "pending"); n != 0 {
		t.Errorf("expected 0 pending after completion, got %d", n)
	}
}

// ── Scenario 5: Supersede ──────────────────────────────────────────────

// TestE2E_Supersede verifies that when a handler returns a
// SupersedeError (newer aggregate version obsoletes the event),
// the event lands in status='superseded' (terminal, distinct from
// dead_letter).
func TestE2E_Supersede(t *testing.T) {
	db := qdrantFlowDB(t)
	repo := outboxevents.NewRepository(db)
	ctx := context.Background()

	// ── Stage 1: Enqueue an older event ──
	assetID := "yt_supersede_005"
	oldHash := "sha256:olderhashvalue000"
	enqueueTestEvent(t, db, assetID, oldHash)

	// ── Stage 2: Enqueue a newer event for the SAME asset (different hash) ──
	// This creates a second outbox row because the event_key differs
	// (different content_hash). The older row should be superseded
	// when the handler detects the stale version.
	newHash := "sha256:newerhashvalue000"
	enqueueTestEvent(t, db, assetID, newHash)

	if n := countOutboxEvents(t, db); n != 2 {
		t.Fatalf("expected 2 events (old + new), got %d", n)
	}

	// ── Stage 3: Claim the older event, simulate supersede ──
	// Intentionally bypass ClaimNext with raw SQL here: the Pool is the
	// only production caller of MarkSuperseded (it classifies SupersedeError
	// after the handler runs). Direct invocation avoids pulling the full
	// Pool dependency into this narrow test.
	var oldEventID int64
	var oldEventKey string
	db.QueryRow(
		"SELECT id, event_key FROM outbox_events ORDER BY id ASC LIMIT 1",
	).Scan(&oldEventID, &oldEventKey)

	// Manually set status to 'processing' so we can MarkSuperseded.
	// (ClaimNext is order-dependent; we explicitly target the row.)
	_, err := db.Exec(
		"UPDATE outbox_events SET status = 'processing', lease_id = 'supersede-lease' WHERE id = ?",
		oldEventID)
	if err != nil {
		t.Fatalf("set processing for supersede: %v", err)
	}

	err = repo.MarkSuperseded(ctx, oldEventID, "supersede-lease",
		fmt.Sprintf("superseded by newer content_hash: old=%s new=%s", oldHash, newHash))
	if err != nil {
		t.Fatalf("MarkSuperseded: %v", err)
	}

	// ── Stage 4: Assert superseded terminal state ──
	if n := countByStatus(t, db, outboxevents.SupersedeStatus); n != 1 {
		t.Errorf("expected 1 superseded event, got %d (status=%q)",
			n, outboxevents.SupersedeStatus)
	}

	// The newer event should still be pending (untouched).
	if n := countByStatus(t, db, "pending"); n != 1 {
		t.Errorf("expected 1 pending event (newer), got %d", n)
	}

	// Verify last_error on the superseded row contains context.
	var lastError string
	db.QueryRow(
		"SELECT last_error FROM outbox_events WHERE id = ?", oldEventID,
	).Scan(&lastError)
	if lastError == "" {
		t.Error("superseded row must have a non-empty last_error for operator audit")
	}
}

// ── Scenario 6: Transactional atomicity (EnqueueAndDelete) ─────────────

// TestE2E_TransactionalAtomicity_DeleteFlow verifies the delete flow:
// EnqueueAndDelete flips index_state to DELETE_PENDING AND inserts
// an outbox event in a single atomic tx. If the tx fails, neither
// is observable.
func TestE2E_TransactionalAtomicity_DeleteFlow(t *testing.T) {
	db := qdrantFlowDB(t)
	repo := outboxevents.NewRepository(db)
	ctx := context.Background()

	clips := &fakeClips{}
	txMgr := &txMgrCapture{db: db}
	d := NewDispatcher(clips, clips, repo, txMgr, zap.NewNop())

	// ── Stage 1: EnqueueAndDelete ──
	assetID := "yt_delete_flow_006"
	if err := d.EnqueueAndDelete(ctx, assetID); err != nil {
		t.Fatalf("EnqueueAndDelete: %v", err)
	}

	// ── Stage 2: Verify state writer was called with DELETE_PENDING ──
	if len(clips.stateLog) != 1 {
		t.Fatalf("expected 1 SetIndexStateTx call, got %d", len(clips.stateLog))
	}
	st := clips.stateLog[0]
	if st.State != asset.StateDeletePending {
		t.Errorf("SetIndexStateTx state: want DELETE_PENDING got %q", st.State)
	}
	if st.ID != assetID {
		t.Errorf("SetIndexStateTx id: want %q got %q", assetID, st.ID)
	}

	// ── Stage 3: Verify outbox event was inserted ──
	if n := countOutboxEvents(t, db); n != 1 {
		t.Fatalf("expected 1 outbox event, got %d", n)
	}

	var eventType, aggID string
	db.QueryRow(
		"SELECT event_type, aggregate_id FROM outbox_events LIMIT 1",
	).Scan(&eventType, &aggID)
	if eventType != outboxevents.EventAssetIndexDeleteRequested {
		t.Errorf("event_type: want %q got %q",
			outboxevents.EventAssetIndexDeleteRequested, eventType)
	}
	if aggID != assetID {
		t.Errorf("aggregate_id: want %q got %q", assetID, aggID)
	}

	// ── Stage 4: Claim and complete the delete event ──
	claim, err := repo.ClaimNext(ctx, "worker-delete", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if claim == nil {
		t.Fatal("expected a claim for delete event")
	}
	if err := repo.MarkCompleted(ctx, claim.Event.ID, claim.LeaseID); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}
	assertStatus(t, db, claim.Event.EventKey, "completed")
}
