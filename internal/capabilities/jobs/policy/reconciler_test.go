package policy

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/finalizer"
)

// setupReconcilerDB creates an in-memory SQLite database with the
// publication_intents, asset_locations, and jobs tables.
func setupReconcilerDB(t *testing.T) (*sql.DB, *finalizer.PublicationIntentReconciler) {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		t.Fatalf("open in-memory DB: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// publication_intents (mirrors migration 118).
	_, err = db.Exec(`
		CREATE TABLE publication_intents (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id           TEXT NOT NULL DEFAULT '',
			attempt          INTEGER NOT NULL DEFAULT 0,
			artifact_id      TEXT NOT NULL DEFAULT '',
			idempotency_key  TEXT NOT NULL DEFAULT '',
			provider         TEXT NOT NULL DEFAULT 'drive',
			state            TEXT NOT NULL DEFAULT 'PREPARED',
			remote_file_id   TEXT NOT NULL DEFAULT '',
			last_error       TEXT NOT NULL DEFAULT '',
			created_at       TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at       TEXT NOT NULL DEFAULT (datetime('now'))
		)
	`)
	if err != nil {
		t.Fatalf("create publication_intents: %v", err)
	}

	// asset_locations (minimal schema).
	_, err = db.Exec(`
		CREATE TABLE asset_locations (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			asset_id      TEXT NOT NULL DEFAULT '',
			location_kind TEXT NOT NULL DEFAULT 'drive',
			uri           TEXT NOT NULL DEFAULT '',
			external_id   TEXT NOT NULL DEFAULT '',
			web_view_link TEXT NOT NULL DEFAULT '',
			download_url  TEXT NOT NULL DEFAULT '',
			mime_type     TEXT NOT NULL DEFAULT '',
			file_size_bytes INTEGER NOT NULL DEFAULT 0,
			file_hash     TEXT NOT NULL DEFAULT '',
			is_primary    INTEGER NOT NULL DEFAULT 1,
			created_at    TEXT NOT NULL DEFAULT '',
			updated_at    TEXT NOT NULL DEFAULT ''
		)
	`)
	if err != nil {
		t.Fatalf("create asset_locations: %v", err)
	}

	// jobs (minimal schema for status checks).
	_, err = db.Exec(`
		CREATE TABLE jobs (
			id         TEXT PRIMARY KEY,
			status     TEXT NOT NULL DEFAULT 'QUEUED',
			type       TEXT NOT NULL DEFAULT '',
			worker_id  TEXT NOT NULL DEFAULT '',
			lease_id   TEXT NOT NULL DEFAULT ''
		)
	`)
	if err != nil {
		t.Fatalf("create jobs: %v", err)
	}

	reconciler := finalizer.NewReconciler(db, nil)
	return db, reconciler
}

func insertIntent(t *testing.T, db *sql.DB, jobID, artifactID, remoteFileID, idempotencyKey string) {
	t.Helper()
	oldTime := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO publication_intents (job_id, artifact_id, remote_file_id, idempotency_key, state, provider, updated_at, created_at)
		 VALUES (?, ?, ?, ?, 'PUBLISHED', 'drive', ?, ?)`,
		jobID, artifactID, remoteFileID, idempotencyKey, oldTime, oldTime,
	)
	if err != nil {
		t.Fatalf("insert intent: %v", err)
	}
}

func insertAssetLocation(t *testing.T, db *sql.DB, assetID, externalID string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO asset_locations (asset_id, location_kind, external_id, created_at, updated_at)
		 VALUES (?, 'drive', ?, datetime('now'), datetime('now'))`,
		assetID, externalID,
	)
	if err != nil {
		t.Fatalf("insert asset_location: %v", err)
	}
}

func insertJob(t *testing.T, db *sql.DB, jobID, status string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT OR REPLACE INTO jobs (id, status, type) VALUES (?, ?, 'test.job')`,
		jobID, status,
	)
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}
}

// ── Test A: Drive caricato, DB fallisce → job FAILED, no asset_location,
//    → reconciler marks ORPHANED. ──────────────────────────────────────

func TestReconciler_OrphanWhenJobFailedAndNoLocation(t *testing.T) {
	db, reconciler := setupReconcilerDB(t)
	ctx := context.Background()

	// Scenario: Drive upload succeeded, but the SQLite transaction
	// failed. The job is FAILED (no retry), no asset_location exists.
	insertJob(t, db, "job-1", "FAILED")
	insertIntent(t, db, "job-1", "artifact-orphan", "drive-file-123", "ik-orphan-1")

	result, err := reconciler.ReconcileOrphans(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("ReconcileOrphans: %v", err)
	}

	if result.Scanned != 1 {
		t.Errorf("Scanned = %d, want 1", result.Scanned)
	}
	if result.MarkedOrphan != 1 {
		t.Errorf("MarkedOrphan = %d, want 1 (job FAILED + no location → orphan)", result.MarkedOrphan)
	}
	if result.SkippedActive != 0 {
		t.Errorf("SkippedActive = %d, want 0", result.SkippedActive)
	}

	var state string
	db.QueryRow(`SELECT state FROM publication_intents WHERE idempotency_key = 'ik-orphan-1'`).Scan(&state)
	if state != "ORPHANED" {
		t.Errorf("state = %q, want ORPHANED", state)
	}

	// Verify the job is still FAILED (not SUCCEEDED).
	var jobStatus string
	db.QueryRow(`SELECT status FROM jobs WHERE id = 'job-1'`).Scan(&jobStatus)
	if jobStatus != "FAILED" {
		t.Errorf("job status = %q, want FAILED (job should NOT be SUCCEEDED after DB failure)", jobStatus)
	}
}

// ── Test B: Drive caricato, DB committed (asset_location exists)
//    → reconciler marks COMMITTED. ───────────────────────────────────

func TestReconciler_CommittedWhenAssetLocationExists(t *testing.T) {
	db, reconciler := setupReconcilerDB(t)
	ctx := context.Background()

	insertJob(t, db, "job-2", "SUCCEEDED")
	insertIntent(t, db, "job-2", "artifact-committed", "drive-file-456", "ik-committed-1")
	insertAssetLocation(t, db, "artifact-committed", "drive-file-456")

	result, err := reconciler.ReconcileOrphans(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("ReconcileOrphans: %v", err)
	}

	if result.MarkedCommitted != 1 {
		t.Errorf("MarkedCommitted = %d, want 1", result.MarkedCommitted)
	}

	var state string
	db.QueryRow(`SELECT state FROM publication_intents WHERE idempotency_key = 'ik-committed-1'`).Scan(&state)
	if state != "COMMITTED" {
		t.Errorf("state = %q, want COMMITTED", state)
	}
}

// ── Test C: Job still RUNNING → reconciler skips (worker may retry). ──

func TestReconciler_SkipsActiveJob(t *testing.T) {
	db, reconciler := setupReconcilerDB(t)
	ctx := context.Background()

	// Job is still RUNNING — the worker may retry the finalization.
	insertJob(t, db, "job-3", "RUNNING")
	insertIntent(t, db, "job-3", "artifact-active", "drive-789", "ik-active")
	// No asset_location — the commit hasn't happened yet.

	result, err := reconciler.ReconcileOrphans(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("ReconcileOrphans: %v", err)
	}

	if result.SkippedActive != 1 {
		t.Errorf("SkippedActive = %d, want 1 (RUNNING job should be skipped)", result.SkippedActive)
	}
	if result.MarkedOrphan != 0 {
		t.Errorf("MarkedOrphan = %d, want 0 (RUNNING job should NOT be orphaned)", result.MarkedOrphan)
	}

	// Verify the intent is still PUBLISHED.
	var state string
	db.QueryRow(`SELECT state FROM publication_intents WHERE idempotency_key = 'ik-active'`).Scan(&state)
	if state != "PUBLISHED" {
		t.Errorf("state = %q, want PUBLISHED (RUNNING job should be left alone)", state)
	}
}

// ── Test D: Idempotent double pass. ──────────────────────────────────

func TestReconciler_IdempotentDoublePass(t *testing.T) {
	db, reconciler := setupReconcilerDB(t)
	ctx := context.Background()

	// Two intents: one with asset_location, one orphaned.
	insertJob(t, db, "job-4a", "FAILED")
	insertJob(t, db, "job-4b", "FAILED")
	insertIntent(t, db, "job-4a", "artifact-with", "drive-000a", "ik-with")
	insertIntent(t, db, "job-4b", "artifact-without", "drive-000b", "ik-without")
	insertAssetLocation(t, db, "artifact-with", "drive-000a")

	result1, err := reconciler.ReconcileOrphans(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("ReconcileOrphans pass 1: %v", err)
	}
	if result1.MarkedOrphan != 1 || result1.MarkedCommitted != 1 {
		t.Errorf("pass 1: MarkedOrphan=%d (want 1), MarkedCommitted=%d (want 1)", result1.MarkedOrphan, result1.MarkedCommitted)
	}

	result2, err := reconciler.ReconcileOrphans(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("ReconcileOrphans pass 2: %v", err)
	}
	if result2.Scanned != 0 {
		t.Errorf("pass 2: Scanned = %d, want 0 (all rows already transitioned)", result2.Scanned)
	}
}

// ── Test E: Recent rows not scanned (race protection). ──────────────

func TestReconciler_RecentRowsNotScanned(t *testing.T) {
	db, reconciler := setupReconcilerDB(t)
	ctx := context.Background()

	nowStr := time.Now().UTC().Format(time.RFC3339)
	insertJob(t, db, "job-5", "FAILED")
	_, err := db.Exec(
		`INSERT INTO publication_intents (job_id, artifact_id, remote_file_id, idempotency_key, state, provider, updated_at, created_at)
		 VALUES (?, ?, ?, ?, 'PUBLISHED', 'drive', ?, ?)`,
		"job-5", "artifact-recent", "drive-recent", "ik-recent", nowStr, nowStr,
	)
	if err != nil {
		t.Fatalf("insert recent intent: %v", err)
	}

	result, err := reconciler.ReconcileOrphans(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("ReconcileOrphans: %v", err)
	}

	if result.Scanned != 0 {
		t.Errorf("Scanned = %d, want 0 (recent rows should not be scanned)", result.Scanned)
	}
}

// ── Test F: Job SUCCEEDED + no asset_location → COMMITTED (intent update). ─

func TestReconciler_CommittedWhenJobSucceeded(t *testing.T) {
	db, reconciler := setupReconcilerDB(t)
	ctx := context.Background()

	// Job SUCCEEDED even though the intent wasn't updated. The commit
	// happened (job completed), the intent just needs updating.
	insertJob(t, db, "job-6", "SUCCEEDED")
	insertIntent(t, db, "job-6", "artifact-succeeded", "drive-999", "ik-succeeded")
	// No asset_location — but the job SUCCEEDED status takes priority.

	result, err := reconciler.ReconcileOrphans(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("ReconcileOrphans: %v", err)
	}

	if result.MarkedCommitted != 1 {
		t.Errorf("MarkedCommitted = %d, want 1 (SUCCEEDED job → COMMITTED)", result.MarkedCommitted)
	}

	var state string
	db.QueryRow(`SELECT state FROM publication_intents WHERE idempotency_key = 'ik-succeeded'`).Scan(&state)
	if state != "COMMITTED" {
		t.Errorf("state = %q, want COMMITTED", state)
	}
}
