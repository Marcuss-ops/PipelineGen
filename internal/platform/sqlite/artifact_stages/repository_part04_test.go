package artifactstages

import (
	"context"
	"errors"
	artifact "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	_ "github.com/mattn/go-sqlite3"
	"testing"
	"time"
)

// TestRepository_InsertWithOutbox_OutboxFailure_RollsBackArtifactRow
// pins the atomicity-in-the-OTHER-direction contract: when the
// outbox_events INSERT fails (here: UNIQUE constraint collision
// on event_key — the canonical unique-index
// ux_outbox_events_event_key rejects the duplicate), the
// co-emitted artifact_stages row MUST be rolled back together
// so NEITHER commits. The test asserts both contract halves:
//   - errors.Is(err, ErrOutboxEmit) → typed-error surface
//   - returned eventKey == "" → no successful commit happened
//   - GetByID returns ErrArtifactStageNotFound → row was ROLLED BACK
//   - direct COUNT(*) probe == 0 → DB-side confirmation
//
// A partial-commit state (artifact row WITHOUT follow-up event)
// would silently orphan the stage from the saga's publisher +
// finalizer steps; this test is the regression sentinel
// against any future regression where InsertWithOutbox loses
// its TX wrapper (e.g., a naive refactor to two sequential
// Exec calls — a plausible-but-wrong simplification).
func TestRepository_InsertWithOutbox_OutboxFailure_RollsBackArtifactRow(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	repo.nowFn = func() time.Time { return nowFixed }
	ctx := context.Background()

	// Pre-seed: an outbox_event with the SAME canonical
	// event_key the InsertWithOutbox will compute. The unique
	// index will reject the duplicate, surfacing as the
	// typed ErrOutboxEmit wrap. (Other fields populated with
	// harmless defaults so the conflicting row simulates
	// "an earlier publish_request emission for the same key
	// already reached pending status".)
	const collidingKey = "stage:job-test-1:art-test-1"
	preSeedTime := nowFixed.UTC().Format(time.RFC3339Nano)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO outbox_events (event_type, aggregate_id, aggregate_type, payload_json, event_key, status, created_at, updated_at)
		   VALUES (?, '', '', '', ?, 'pending', ?, ?)`,
		eventTypeForTest, collidingKey, preSeedTime, preSeedTime,
	); err != nil {
		t.Fatalf("pre-seed colliding outbox row: %v", err)
	}

	stage := validStage()
	eventKey, err := repo.InsertWithOutbox(ctx, stage, eventTypeForTest, payloadForTest)
	if err == nil {
		t.Fatalf("InsertWithOutbox with colliding event_key: expected non-nil error, got eventKey=%q (TX SHOULD have failed)", eventKey)
	}
	if !errors.Is(err, artifact.ErrOutboxEmit) {
		t.Errorf("err = %v, want ErrOutboxEmit (typed-error contract for outbox INSERT failure)", err)
	}

	// 1. Returned eventKey MUST be empty on failure (no
	//    successful commit happened — the caller cannot
	//    log a non-existent eventKey).
	if eventKey != "" {
		t.Errorf("returned eventKey = %q, want %q (no commit happened)", eventKey, "")
	}

	// 2. CRITICAL REGRESSION SENTINEL: artifact_stages row
	//    MUST NOT exist — the TX was rolled back atomically.
	//    A non-rolled-back row would orphan the stage from
	//    the publisher + finalizer saga (the row would scan
	//    but no follow-up event would fire).
	got, getErr := repo.GetByID(ctx, stage.ID)
	if got != nil {
		t.Errorf("artifact_stages row IS persisted after failed InsertWithOutbox (id=%q state=%q) — TX rollback REGRESSION: row should be rolled back", got.ID, got.State)
	}
	if !errors.Is(getErr, artifact.ErrArtifactStageNotFound) {
		t.Errorf("GetByID after failed InsertWithOutbox: err = %v, want ErrArtifactStageNotFound (row was rolled back)", getErr)
	}

	// 3. Direct DB-side confirmation: COUNT(*) probe against
	//    artifact_stages for the canonical stage ID. Belt-
	//    and-suspenders check independent of the
	//    application-layer GetByID sentinel mapping.
	var rowCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM artifact_stages WHERE id = ?`, stage.ID).Scan(&rowCount); err != nil {
		t.Fatalf("COUNT(artifact_stages by id): %v", err)
	}
	if rowCount != 0 {
		t.Errorf("artifact_stages row count for id=%q = %d, want 0 (TX was not rolled back — atomicity REGRESSION)", stage.ID, rowCount)
	}

	// 4. The colliding outbox row is STILL present (we
	//    deliberately do NOT clean it up — production's
	//    retry-pipeline expects the prior failed emission
	//    remains for diagnostics). The application's
	//    job_id lookup would find it on a re-attempt via
	//    event_key dedup; the test confirms the pre-seed
	//    was not inadvertently cleaned by the rollback.
	var preSeedCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox_events WHERE event_key = ?`, collidingKey).Scan(&preSeedCount); err != nil {
		t.Fatalf("COUNT(outbox_events by pre-seeded event_key): %v", err)
	}
	if preSeedCount != 1 {
		t.Errorf("pre-seeded outbox row count for event_key=%q = %d, want 1 (pre-seed should remain untouched by TX rollback)", collidingKey, preSeedCount)
	}
}
