package jobs

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	_ "github.com/mattn/go-sqlite3" // blank import: registers the sqlite3 driver for sql.Open
	"velox/go-master/internal/media/models"
)

// setupTransitionDB opens a SQLite in-memory database, applies the
// minimal schema needed by Transition tests, and returns the *sql.DB
// handle. It does NOT depend on internal/storage's NewTestDBWithSchema
// so this file can be run even when that pre-existing helper is
// unavailable (see PR-1.5 backlog: NewTestDBWithSchema was missing).
func setupTransitionDB(t *testing.T) *sql.DB {
	t.Helper()
	schema := `
	CREATE TABLE jobs (
		id TEXT PRIMARY KEY,
		type TEXT NOT NULL,
		status TEXT NOT NULL,
		priority INTEGER NOT NULL DEFAULT 0,
		project TEXT DEFAULT '',
		video_name TEXT DEFAULT '',
		active_key TEXT DEFAULT '',
		correlation_id TEXT NOT NULL DEFAULT '',
		payload_json TEXT NOT NULL DEFAULT '{}',
		result_json TEXT NOT NULL DEFAULT '{}',
		progress INTEGER NOT NULL DEFAULT 0,
		error TEXT DEFAULT '',
		retry_count INTEGER NOT NULL DEFAULT 0,
		max_retries INTEGER NOT NULL DEFAULT 3,
		worker_id TEXT DEFAULT '',
		lease_expiry TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		started_at TEXT,
		completed_at TEXT,
		cancelled_at TEXT,
		revision INTEGER NOT NULL DEFAULT 1
	);
	`
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite memory db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(context.Background(), schema); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return db
}

// insertTestJob writes a job row directly into the DB so the test
// can craft exact revision + status pre-conditions. It returns the
// created-at / updated-at values used so callers can compare.
func insertTestJob(t *testing.T, db *sql.DB, job *models.Job) {
	t.Helper()
	now := time.Now().UTC()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = now
	}
	if job.Revision == 0 {
		job.Revision = 1
	}
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO jobs
		(id, type, status, priority, project, video_name, active_key,
		 correlation_id, payload_json, result_json, progress, error, retry_count, max_retries,
		 worker_id, lease_expiry, created_at, updated_at, started_at, completed_at, cancelled_at,
		 revision)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.Type, job.Status, job.Priority, job.Project, job.VideoName, job.ActiveKey,
		job.CorrelationID, string(job.Payload), "{}", job.Progress, job.Error,
		job.RetryCount, job.MaxRetries, job.WorkerID, nil,
		job.CreatedAt.Format(time.RFC3339), job.UpdatedAt.Format(time.RFC3339),
		nil, nil, nil, job.Revision,
	)
	if err != nil {
		t.Fatalf("insert test job: %v", err)
	}
}

// TestTransition_OptimisticLock_HappyPath verifies that a Transition
// with the correct expected revision + expected status fires the
// UPDATE atomically and bumps the revision counter on the row.
func TestTransition_OptimisticLock_HappyPath(t *testing.T) {
	db := setupTransitionDB(t)
	repo := NewRepository(db, zap.NewNop())
	ctx := context.Background()

	job := &models.Job{
		ID:     "happy-1",
		Type:   "test.transition",
		Status: models.StatusQueued,
	}
	insertTestJob(t, db, job)

	updated, err := repo.Transition(ctx, TransitionRequest{
		JobID:            job.ID,
		ExpectedRevision: 1,
		ExpectedStatus:   models.StatusQueued,
		NewStatus:        models.StatusRunning,
		Updates: map[string]any{
			"worker_id":    "worker-A",
			"lease_expiry": time.Now().Add(5 * time.Minute),
			"started_at":   time.Now(),
		},
	})
	if err != nil {
		t.Fatalf("expected transition to succeed, got: %v", err)
	}
	if updated.Status != models.StatusRunning {
		t.Errorf("expected status=running, got %s", updated.Status)
	}
	if updated.Revision != 2 {
		t.Errorf("expected revision to bump to 2, got %d", updated.Revision)
	}
	if updated.WorkerID != "worker-A" {
		t.Errorf("expected worker_id=worker-A, got %q", updated.WorkerID)
	}
}

// TestTransition_IdempotentRace verifies that two Transition calls
// with the same (ExpectedRevision, ExpectedStatus) token on a
// sequential run cannot both succeed: the second call must observe
// the post-bump revision and return ErrOptimisticLockFailed.
//
// In single-process SQLite, this is exercised in series rather than
// concurrently because the claimMu serialises parallel writers; the
// contract is the same either way.
func TestTransition_IdempotentRace(t *testing.T) {
	db := setupTransitionDB(t)
	repo := NewRepository(db, zap.NewNop())
	ctx := context.Background()

	job := &models.Job{
		ID:     "race-1",
		Type:   "test.transition",
		Status: models.StatusQueued,
	}
	insertTestJob(t, db, job)

	// First transition fires successfully.
	_, err := repo.Transition(ctx, TransitionRequest{
		JobID:            job.ID,
		ExpectedRevision: 1,
		ExpectedStatus:   models.StatusQueued,
		NewStatus:        models.StatusRunning,
	})
	if err != nil {
		t.Fatalf("first transition should succeed, got: %v", err)
	}

	// Second transition with the SAME expected revision must fail
	// with ErrOptimisticLockFailed. We wrap to check the sentinel via
	// errors.Is, which the Transition method supports because it
	// returns fmt.Errorf("%w: ...", ErrOptimisticLockFailed).
	_, err = repo.Transition(ctx, TransitionRequest{
		JobID:            job.ID,
		ExpectedRevision: 1, // stale: row is now revision 2
		ExpectedStatus:   models.StatusRunning,
		NewStatus:        models.StatusCompleted,
	})
	if err == nil {
		t.Fatal("expected ErrOptimisticLockFailed, got nil")
	}
	if !errors.Is(err, ErrOptimisticLockFailed) {
		t.Errorf("expected ErrOptimisticLockFailed, got: %v", err)
	}
}

// TestTransition_DisallowedKey verifies the defence-in-depth whitelist:
// unknown keys in Updates are rejected BEFORE the SQL is built, so a
// caller cannot inject arbitrary SET clauses.
func TestTransition_DisallowedKey(t *testing.T) {
	db := setupTransitionDB(t)
	repo := NewRepository(db, zap.NewNop())
	ctx := context.Background()

	job := &models.Job{
		ID:     "disallow-1",
		Type:   "test.transition",
		Status: models.StatusQueued,
	}
	insertTestJob(t, db, job)

	_, err := repo.Transition(ctx, TransitionRequest{
		JobID:            job.ID,
		ExpectedRevision: 1,
		ExpectedStatus:   models.StatusQueued,
		NewStatus:        models.StatusRunning,
		Updates: map[string]any{
			// 'injection_col' is intentionally not in transitionUpdateKeys.
			// If a future maintainer adds it without auditing the
			// whitelist, this test fails.
			"injection_col": "DROP TABLE jobs; --",
			"worker_id":     "worker-safe",
		},
	})
	if err == nil {
		t.Fatal("expected error for disallowed key, got nil")
	}
	if !strings.Contains(err.Error(), "injection_col") {
		t.Errorf("expected error message to name the disallowed key, got: %v", err)
	}
	// Verify the row was NOT updated.
	got, err := repo.Get(ctx, job.ID)
	if err != nil || got == nil {
		t.Fatalf("post-failure Get: job=%v err=%v", got, err)
	}
	if got.WorkerID != "" {
		t.Errorf("worker_id should be empty after rejected transition, got: %q", got.WorkerID)
	}
	if got.Status != models.StatusQueued {
		t.Errorf("status should still be queued after rejected transition, got: %s", got.Status)
	}
}

// TestTransition_StateMachineRejected verifies that the state-machine
// guard fires BEFORE the SQL UPDATE for terminal → non-terminal jumps.
// Trying to transition completed → completed (which models.TransitionJob
// rejects) must return an error and leave the row unchanged.
func TestTransition_StateMachineRejected(t *testing.T) {
	db := setupTransitionDB(t)
	repo := NewRepository(db, zap.NewNop())
	ctx := context.Background()

	job := &models.Job{
		ID:     "sm-1",
		Type:   "test.transition",
		Status: models.StatusCompleted,
	}
	insertTestJob(t, db, job)

	_, err := repo.Transition(ctx, TransitionRequest{
		JobID:            job.ID,
		ExpectedRevision: 1,
		ExpectedStatus:   models.StatusCompleted,
		// Terminal status can't go anywhere; this is rejected by the
		// state-machine guard before the SQL is even composed.
		NewStatus: models.StatusFailed,
	})
	if err == nil {
		t.Fatal("expected state-machine rejection, got nil")
	}
	if !strings.Contains(err.Error(), "state machine") {
		t.Errorf("expected error to mention state machine, got: %v", err)
	}
	// Row untouched.
	got, err := repo.Get(ctx, job.ID)
	if err != nil || got == nil {
		t.Fatalf("post-failure Get: job=%v err=%v", got, err)
	}
	if got.Status != models.StatusCompleted {
		t.Errorf("status should still be completed, got: %s", got.Status)
	}
	if got.Revision != 1 {
		t.Errorf("revision should still be 1, got: %d", got.Revision)
	}
}

// TestTransition_NilPointerClearsColumn verifies the contract that
// Retry relies on: a typed nil pointer in Updates clears the column
// at the SQL level (NULL), not the string "" or `0001-01-01`. This is
// the fragility-fix from PR-1.5 (var clearLease in Retry).
func TestTransition_NilPointerClearsColumn(t *testing.T) {
	db := setupTransitionDB(t)
	repo := NewRepository(db, zap.NewNop())
	ctx := context.Background()

	job := &models.Job{
		ID:     "nil-ptr-1",
		Type:   "test.transition",
		Status: models.StatusFailed,
	}
	insertTestJob(t, db, job)

	var clearLease *time.Time // nil → SQL NULL
	updated, err := repo.Transition(ctx, TransitionRequest{
		JobID:            job.ID,
		ExpectedRevision: 1,
		ExpectedStatus:   models.StatusFailed,
		NewStatus:        models.StatusQueued,
		Updates: map[string]any{
			"retry_count":  1,
			"lease_expiry": clearLease,
			"worker_id":    "",
		},
	})
	if err != nil {
		t.Fatalf("expected Transition to succeed with nil *time.Time, got: %v", err)
	}
	if updated.Status != models.StatusQueued {
		t.Errorf("expected status=queued, got %s", updated.Status)
	}
	// Verify the row in DB has NULL lease_expiry.
	var leaseRaw sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT lease_expiry FROM jobs WHERE id = ?`, job.ID).Scan(&leaseRaw); err != nil {
		t.Fatalf("scan lease_expiry: %v", err)
	}
	if leaseRaw.Valid {
		t.Errorf("expected NULL lease_expiry after nil-pointer update, got: %q", leaseRaw.String)
	}
	if updated.Revision != 2 {
		t.Errorf("expected revision=2 after Retry, got %d", updated.Revision)
	}
}
