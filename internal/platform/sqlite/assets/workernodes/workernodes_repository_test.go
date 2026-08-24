package workernodes

import (
	"context"
	"database/sql"
	"testing"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	_ "github.com/mattn/go-sqlite3"
)

// openWorkerNodesTestDB opens an in-memory SQLite instance with
// the canonical worker_nodes schema (mirrors migration
// 057_worker_nodes_and_workflows.sql). The production migration
// uses CREATE TABLE IF NOT EXISTS so the schema is stable across
// test runs.
//
// Per AGENTS.md §"Database rules", driver is locked on
// mattn/go-sqlite3 — the import above is intentional and should
// not be changed by future contributors without a corresponding
// migration to a different driver + godlike/06 update.
func openWorkerNodesTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite3 :memory: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS worker_nodes (
			id                  TEXT PRIMARY KEY,
			name                TEXT NOT NULL,
			status              TEXT NOT NULL,
			session_id          TEXT NOT NULL,
			session_expires_at  TEXT NOT NULL,
			capabilities_json   TEXT NOT NULL,
			version             TEXT NOT NULL,
			hostname            TEXT NOT NULL,
			last_seen_at        TEXT NOT NULL,
			created_at          TEXT NOT NULL,
			updated_at          TEXT NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("create worker_nodes %v", err)
	}
	return db
}

func validTestRegisterCmd() job.RegisterWorkerCommand {
	return job.RegisterWorkerCommand{
		WorkerID: "worker-A",
		Name:     "worker-A",
		Version:  "v1.0.0",
		Hostname: "host-A",
		Capabilities: job.WorkerCapabilities{
			JobTypes: []string{"test.job_type"},
		},
	}
}

// TestWorkerNodesRepository_Register_IdempotentForActiveSession
// is the regression pin for PR-FIX-WORKER-SESSION-REGISTER-RACE
// (2026-07-08). Pre-fix, the ON CONFLICT clause unconditionally
// overwrote session_id + session_expires_at with the new
// uuid.NewString(), so a periodic re-Register from a competing
// process (doctor healthcheck, hot-spare, recovery re-register)
// silently invalidated the active worker's in-memory session_id.
// The next heartbeat would return sql.ErrNoRows (WHERE id=? AND
// session_id=? no longer matches), and the next claim would
// surface "worker session is invalid or expired" via the
// broker's ensureSession → IsSessionActive check.
//
// Post-fix: when the existing row is still status='active' AND
// session_expires_at is in the future, the ON CONFLICT preserves
// the OLD session_id. Two consecutive Register calls for the
// same WorkerID within the TTL MUST return the SAME session_id.
func TestWorkerNodesRepository_Register_IdempotentForActiveSession(t *testing.T) {
	db := openWorkerNodesTestDB(t)
	repo := NewWorkerNodesRepository(db)
	ctx := context.Background()

	cmd := validTestRegisterCmd()
	first, err := repo.Register(ctx, cmd)
	if err != nil {
		t.Fatalf("first Register err: %v", err)
	}
	if first.SessionID == "" {
		t.Fatalf("first Register returned empty session_id")
	}

	// Second Register with the SAME WorkerID while the first
	// session is still active. Per the idempotency contract,
	// the returned session_id MUST be byte-equivalent to the
	// first one (the DB preserves the old session_id, the
	// post-UPSERT Get returns the canonical state).
	second, err := repo.Register(ctx, cmd)
	if err != nil {
		t.Fatalf("second Register err: %v", err)
	}
	if second.SessionID != first.SessionID {
		t.Errorf("idempotent Register returned DIFFERENT session_id: first=%q second=%q (pre-fix bug: competing process re-Register nukes active session)",
			first.SessionID, second.SessionID)
	}
	if !second.SessionExpiresAt.Equal(first.SessionExpiresAt) {
		t.Errorf("idempotent Register changed SessionExpiresAt: first=%v second=%v", first.SessionExpiresAt, second.SessionExpiresAt)
	}
}

// TestWorkerNodesRepository_Register_MintsNewSessionWhenExpired
// is the inverse contract: when the existing session HAS
// expired (session_expires_at in the past), Register MUST mint
// a fresh session_id. This is the takeover path — a previously
// dead worker can reclaim its WorkerID after its old session
// expired, and a real new worker can take over a WorkerID whose
// previous worker died.
func TestWorkerNodesRepository_Register_MintsNewSessionWhenExpired(t *testing.T) {
	db := openWorkerNodesTestDB(t)
	repo := NewWorkerNodesRepository(db)
	ctx := context.Background()

	cmd := validTestRegisterCmd()
	first, err := repo.Register(ctx, cmd)
	if err != nil {
		t.Fatalf("first Register err: %v", err)
	}

	// Manually expire the session by setting session_expires_at
	// to a past RFC3339 timestamp. This simulates a session that
	// has run past its TTL without a heartbeat refresh.
	pastRFC3339 := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE worker_nodes SET session_expires_at = ? WHERE id = ?`, pastRFC3339, cmd.WorkerID); err != nil {
		t.Fatalf("expire session: %v", err)
	}

	// Second Register: the existing session is now expired, so
	// a new session_id MUST be minted (takeover path).
	second, err := repo.Register(ctx, cmd)
	if err != nil {
		t.Fatalf("second Register (post-expiry) err: %v", err)
	}
	if second.SessionID == first.SessionID {
		t.Errorf("Register-after-expiry returned SAME session_id: %q (expected a fresh session_id — the takeover path is broken)", second.SessionID)
	}
	if second.SessionID == "" {
		t.Errorf("Register-after-expiry returned empty session_id")
	}
	if second.SessionExpiresAt.Before(first.SessionExpiresAt) {
		t.Errorf("Register-after-expiry returned an EARLIER SessionExpiresAt: first=%v second=%v", first.SessionExpiresAt, second.SessionExpiresAt)
	}
}

// TestWorkerNodesRepository_Register_MintsNewSessionWhenStatusInactive
// is the second takeover path: when the existing row is
// status != 'active' (e.g. status='inactive' after a graceful
// shutdown, status='dead' after a hard kill), Register MUST
// mint a fresh session_id. This is independent from
// session_expires_at (a worker could be marked inactive
// before its TTL expires, e.g. via an admin command).
func TestWorkerNodesRepository_Register_MintsNewSessionWhenStatusInactive(t *testing.T) {
	db := openWorkerNodesTestDB(t)
	repo := NewWorkerNodesRepository(db)
	ctx := context.Background()

	cmd := validTestRegisterCmd()
	first, err := repo.Register(ctx, cmd)
	if err != nil {
		t.Fatalf("first Register err: %v", err)
	}

	// Mark the worker as inactive while keeping session_expires_at
	// in the future. The CASE clause explicitly checks status,
	// so this path tests the status-only branch.
	if _, err := db.Exec(`UPDATE worker_nodes SET status = 'inactive' WHERE id = ?`, cmd.WorkerID); err != nil {
		t.Fatalf("mark inactive: %v", err)
	}

	second, err := repo.Register(ctx, cmd)
	if err != nil {
		t.Fatalf("second Register (post-inactive) err: %v", err)
	}
	if second.SessionID == first.SessionID {
		t.Errorf("Register-after-inactive returned SAME session_id: %q (expected a fresh session_id — the status-only takeover path is broken)", second.SessionID)
	}
}

// TestWorkerNodesRepository_Register_PreservesCapabilitiesOnIdempotent
// pins a side-effect of the idempotency contract: the capabilities
// (and other mutable fields: name, version, hostname, last_seen_at)
// MUST be refreshed even when the session_id is preserved. A
// competing process upgrading its capabilities should still see
// the new caps reflected after a re-Register.
func TestWorkerNodesRepository_Register_PreservesCapabilitiesOnIdempotent(t *testing.T) {
	db := openWorkerNodesTestDB(t)
	repo := NewWorkerNodesRepository(db)
	ctx := context.Background()

	cmd := validTestRegisterCmd()
	first, err := repo.Register(ctx, cmd)
	if err != nil {
		t.Fatalf("first Register err: %v", err)
	}

	// Re-Register with UPDATED version + capabilities (the
	// worker's binary upgraded + job type set expanded).
	cmd2 := cmd
	cmd2.Version = "v2.0.0"
	cmd2.Capabilities.JobTypes = []string{"test.job_type", "test.new_job_type"}
	cmd2.Capabilities.GPU = true
	second, err := repo.Register(ctx, cmd2)
	if err != nil {
		t.Fatalf("second Register err: %v", err)
	}
	if second.SessionID != first.SessionID {
		t.Errorf("session_id should still be preserved on idempotent re-Register: first=%q second=%q", first.SessionID, second.SessionID)
	}
	if second.Version != "v2.0.0" {
		t.Errorf("Version not refreshed: got %q, want %q", second.Version, "v2.0.0")
	}
	if !second.Capabilities.GPU {
		t.Errorf("Capabilities.GPU not refreshed: got %v, want true", second.Capabilities)
	}
	if len(second.Capabilities.JobTypes) != 2 {
		t.Errorf("Capabilities.JobTypes not refreshed: got %v, want 2 entries", second.Capabilities.JobTypes)
	}
}
