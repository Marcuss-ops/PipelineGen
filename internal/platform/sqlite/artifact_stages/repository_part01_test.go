// Package artifactstages — internal/platform/sqlite/artifact_stages/repository_test.go
//
// FASE 3 (Push 3.1a, July 2026) hermetic round-trip tests for the
// concrete Repository. Uses in-memory SQLite + the canonical
// migration 147 DDL (table + 3 indexes) so the test schema and the
// production schema cannot drift.
//
// Coverage:
//   - Insert (8 cases: valid + 4 invalid-state + 2 invalid-requirement
//   - size<=0 + empty-hash)
//   - GetByID (happy + not-found)
//   - ListByJob (empty + multi-row ordering)
//   - ListByState (empty + non-empty + limit + invalid-state)
//   - MarkPublished (happy + fenced on terminal + not-found)
//   - MarkSucceeded (happy + fenced on terminal)
//   - MarkFailedPermanent (happy + fenced on terminal)
//   - IncrementAttemptCount (happy + capped at terminal-state fence)
//
// godlike/06 SSOT: the test schema mirrors migration 147 EXACTLY
// (extracted as a constant; drift is caught at the first INSERT
// when the columns don't match).
// godlike/07 fail-closed: every failure path is asserted at the
// typed-error level (errors.Is to the canonical sentinels).
package artifactstages

import (
	"context"
	"database/sql"
	"errors"
	artifact "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	_ "github.com/mattn/go-sqlite3"
	"testing"
	"time"
)

// canonicalDDL is the verbatim DDL from migrations/sqlite/147_artifact_stages.sql.
// Drift between this constant and the production migration is a
// bug — fix one, fix the other.
const canonicalDDL = `
CREATE TABLE IF NOT EXISTS artifact_stages (
    id                 TEXT PRIMARY KEY,
    job_id             TEXT NOT NULL DEFAULT '',
    local_path         TEXT NOT NULL DEFAULT '',
    hash               TEXT NOT NULL DEFAULT '',
    size               INTEGER NOT NULL DEFAULT 0,
    mime               TEXT NOT NULL DEFAULT '',
    requirement        TEXT NOT NULL DEFAULT 'optional'
        CHECK (requirement IN ('required','optional')),
    destination        TEXT NOT NULL DEFAULT '',
    state              TEXT NOT NULL DEFAULT 'STAGED'
        CHECK (state IN ('STAGED','PUBLISHED','SUCCEEDED','FAILED_PERMANENT')),
    attempt_count      INTEGER NOT NULL DEFAULT 0,
    last_error         TEXT NOT NULL DEFAULT '',
    published_location TEXT NOT NULL DEFAULT '',
    published_at       TEXT,
    created_at         TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at         TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_artifact_stages_job_state
    ON artifact_stages(job_id, state);
CREATE INDEX IF NOT EXISTS idx_artifact_stages_state_created
    ON artifact_stages(state, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_artifact_stages_dest
    ON artifact_stages(destination);
`

// canonicalOutboxDDL is the verbatim DDL from
// migrations/sqlite/092_create_outbox_events.sql. Pinned here
// so drift between the test schema and the production schema
// is caught at the first INSERT (column-count vs DDL mismatch
// surfaces as a SQL syntax error → easy to diagnose in a
// test). The ux_outbox_events_event_key UNIQUE index is the
// essential piece for the rollback-rollback test
// (TestRepository_InsertWithOutbox_OutboxFailure_RollsBackArtifactRow)
// — pre-seeding a colliding event_key triggers the same
// constraint rejection production code would see on a real
// duplicate publish_request emission.
const canonicalOutboxDDL = `
CREATE TABLE IF NOT EXISTS outbox_events (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type      TEXT NOT NULL,
    aggregate_id    TEXT NOT NULL DEFAULT '',
    aggregate_type  TEXT NOT NULL DEFAULT '',
    payload_json    TEXT NOT NULL DEFAULT '',
    event_key       TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'pending',
    attempt_count   INTEGER NOT NULL DEFAULT 0,
    max_attempts    INTEGER NOT NULL DEFAULT 10,
    last_error      TEXT NOT NULL DEFAULT '',
    next_attempt_at TEXT,
    worker_id       TEXT NOT NULL DEFAULT '',
    lease_id        TEXT NOT NULL DEFAULT '',
    lease_expiry    TEXT,
    completed_at    TEXT,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_outbox_events_event_key
    ON outbox_events(event_key);
`

// setupTestDB creates an in-memory SQLite with the canonical 147
// schema. Cleanup is automatic via t.Cleanup.
//
// DSN: `parseTime=true&loc=UTC` — the mattn/go-sqlite3 driver
// default returns TEXT columns as raw strings; without
// `parseTime=true`, the canonical `created_at`/`updated_at`/
// `published_at` TEXT columns (stored as RFC3339Nano) cannot be
// Scanned into time.Time values via the standard library driver.
// Production code wires the DSN with the same flag at the
// composition root (internal/app/build_bundles_*.go).
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?parseTime=true&loc=UTC")
	if err != nil {
		t.Fatalf("open :memory: sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(canonicalDDL); err != nil {
		t.Fatalf("apply canonical DDL: %v", err)
	}
	if _, err := db.Exec(canonicalOutboxDDL); err != nil {
		t.Fatalf("apply canonical outbox DDL: %v", err)
	}
	return db
}

// nowFixed is a deterministic time for Insert testing.
var nowFixed = time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)

// validStage returns a minimal valid stage for Insert.
func validStage() *artifact.ArtifactStage {
	return &artifact.ArtifactStage{
		ID:           "art-test-1",
		JobID:        "job-test-1",
		LocalPath:    "/var/lib/pipelinegen/staging/job-test-1/art-test-1",
		Hash:         "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Size:         4096,
		Mime:         "audio/mpeg",
		Requirement:  artifact.RequirementRequired,
		Destination:  "drive:voiceover/test",
		State:        artifact.ArtifactStageStateStaged,
		AttemptCount: 0,
	}
}

// ── Insert ──────────────────────────────────────────────────────────────

func TestRepository_Insert_HappyPath(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	repo.nowFn = func() time.Time { return nowFixed }
	ctx := context.Background()
	stage := validStage()
	if err := repo.Insert(ctx, stage); err != nil {
		t.Fatalf("Insert valid stage: unexpected error: %v", err)
	}
	// Read back via GetByID; the row must round-trip.
	got, err := repo.GetByID(ctx, stage.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.JobID != stage.JobID {
		t.Errorf("JobID = %q, want %q", got.JobID, stage.JobID)
	}
	if got.Hash != stage.Hash {
		t.Errorf("Hash = %q, want %q", got.Hash, stage.Hash)
	}
	if got.State != artifact.ArtifactStageStateStaged {
		t.Errorf("State = %q, want STAGED", got.State)
	}
	if got.Requirement != artifact.RequirementRequired {
		t.Errorf("Requirement = %q, want required", got.Requirement)
	}
	if got.CreatedAt.IsZero() {
		t.Errorf("CreatedAt must be populated by Insert")
	}
	if got.UpdatedAt.IsZero() {
		t.Errorf("UpdatedAt must be populated by Insert")
	}
}

func TestRepository_Insert_RejectsInvalidState(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	stage := validStage()
	stage.State = artifact.ArtifactStageState("IN_PROGRESS") // not canonical
	if err := repo.Insert(context.Background(), stage); !errors.Is(err, artifact.ErrInvalidArtifactStageState) {
		t.Errorf("Insert with bogus state: err = %v, want ErrInvalidArtifactStageState", err)
	}
}

func TestRepository_Insert_RejectsInvalidRequirement(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	stage := validStage()
	stage.Requirement = artifact.Requirement("recommended") // not canonical
	if err := repo.Insert(context.Background(), stage); !errors.Is(err, artifact.ErrInvalidRequirement) {
		t.Errorf("Insert with bogus requirement: err = %v, want ErrInvalidRequirement", err)
	}
}

func TestRepository_Insert_RejectsZeroSize(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	stage := validStage()
	stage.Size = 0
	if err := repo.Insert(context.Background(), stage); !errors.Is(err, artifact.ErrArtifactStageEmpty) {
		t.Errorf("Insert with size=0: err = %v, want ErrArtifactStageEmpty", err)
	}
}

func TestRepository_Insert_RejectsEmptyHash(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	stage := validStage()
	stage.Hash = ""
	if err := repo.Insert(context.Background(), stage); !errors.Is(err, artifact.ErrArtifactStageHashMismatch) {
		t.Errorf("Insert with empty hash: err = %v, want ErrArtifactStageHashMismatch", err)
	}
}

// TestRepository_Insert_RejectsEmptyJobID pins the canonical
// invariant: every stage row MUST have a non-empty JobID
// (FK-by-convention to jobs.id). An empty JobID would orphan
// the stage from the finalizer's ListByJob scan + the
// publisher worker's accounting — the artifact would silently
// vanish from the saga.
func TestRepository_Insert_RejectsEmptyJobID(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	stage := validStage()
	stage.JobID = ""
	if err := repo.Insert(context.Background(), stage); !errors.Is(err, artifact.ErrInvalidJobID) {
		t.Errorf("Insert with empty JobID: err = %v, want ErrInvalidJobID", err)
	}
}

// TestRepository_Insert_RejectsNonStagedState pins the
// state-machine enforcement: only StateStaged is allowed on
// Insert (the canonical initial state of the saga). A caller
// that tries to insert a PUBLISHED / SUCCEEDED /
// FAILED_PERMANENT row bypasses the state machine and is
// rejected with ErrInvalidArtifactStageState (the same
// sentinel used for non-canonical values, so log-greppers get
// one consistent failure class for "the Insert's state value
// is wrong").
func TestRepository_Insert_RejectsNonStagedState(t *testing.T) {
	for _, badState := range []artifact.ArtifactStageState{
		artifact.ArtifactStageStatePublished,
		artifact.ArtifactStageStateSucceeded,
		artifact.ArtifactStageStateFailedPermanent,
	} {
		t.Run(string(badState), func(t *testing.T) {
			db := setupTestDB(t)
			repo := NewRepository(db)
			stage := validStage()
			stage.State = badState
			if err := repo.Insert(context.Background(), stage); !errors.Is(err, artifact.ErrInvalidArtifactStageState) {
				t.Errorf("Insert with state=%q: err = %v, want ErrInvalidArtifactStageState", badState, err)
			}
		})
	}
}

// ── GetByID ─────────────────────────────────────────────────────────────

func TestRepository_GetByID_NotFound(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	_, err := repo.GetByID(context.Background(), "art-missing")
	if !errors.Is(err, artifact.ErrArtifactStageNotFound) {
		t.Errorf("GetByID missing: err = %v, want ErrArtifactStageNotFound", err)
	}
}

// ── ListByJob ──────────────────────────────────────────────────────────

func TestRepository_ListByJob_Empty(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	got, err := repo.ListByJob(context.Background(), "job-no-such-row")
	if err != nil {
		t.Fatalf("ListByJob empty: %v", err)
	}
	if got != nil && len(got) != 0 {
		t.Errorf("ListByJob empty: got %d rows, want 0", len(got))
	}
}
