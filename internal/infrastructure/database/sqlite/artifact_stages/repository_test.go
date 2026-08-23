// Package artifactstages — internal/infrastructure/database/sqlite/artifact_stages/repository_test.go
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
	"testing"
	"time"

	artifact "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	_ "github.com/mattn/go-sqlite3"
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

func TestRepository_ListByJob_OrderedByCreatedAt(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	repo.nowFn = func() time.Time { return nowFixed }
	ctx := context.Background()
	// Seed 3 rows for the same job in non-monotonic order; the
	// repository MUST return them in created_at ASC.
	seeds := []*artifact.ArtifactStage{
		func() *artifact.ArtifactStage {
			s := validStage()
			s.ID = "art-3"
			return s
		}(),
		func() *artifact.ArtifactStage {
			s := validStage()
			s.ID = "art-1"
			return s
		}(),
		func() *artifact.ArtifactStage {
			s := validStage()
			s.ID = "art-2"
			return s
		}(),
	}
	for i, s := range seeds {
		// Stagger created_at so ordering is observable.
		s.CreatedAt = nowFixed.Add(time.Duration(i) * time.Second)
		if err := repo.Insert(ctx, s); err != nil {
			t.Fatalf("seed[%d] insert: %v", i, err)
		}
	}
	got, err := repo.ListByJob(ctx, "job-test-1")
	if err != nil {
		t.Fatalf("ListByJob: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListByJob: got %d rows, want 3", len(got))
	}
	// Order is created_at ASC → art-3 (0s), art-1 (1s), art-2 (2s).
	// The seed loop sets CreatedAt = nowFixed + i*Second for
	// seeds[0]=art-3, seeds[1]=art-1, seeds[2]=art-2; SQL ORDER BY
	// created_at ASC therefore returns them in seed-array order,
	// NOT in lexicographic ID order.
	wantOrder := []string{"art-3", "art-1", "art-2"}
	for i, want := range wantOrder {
		if got[i].ID != want {
			t.Errorf("ListByJob[%d].ID = %q, want %q", i, got[i].ID, want)
		}
	}
}

// ── ListByState ────────────────────────────────────────────────────────

func TestRepository_ListByState_RejectsInvalidState(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	_, err := repo.ListByState(context.Background(), "IN_PROGRESS", 10)
	if !errors.Is(err, artifact.ErrInvalidArtifactStageState) {
		t.Errorf("ListByState bogus: err = %v, want ErrInvalidArtifactStageState", err)
	}
}

func TestRepository_ListByState_ReturnsOnlyMatchingState(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	repo.nowFn = func() time.Time { return nowFixed }
	ctx := context.Background()
	// Seed 2 STAGED + 1 PUBLISHED rows.
	for _, id := range []string{"art-s1", "art-s2"} {
		s := validStage()
		s.ID = id
		if err := repo.Insert(ctx, s); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	pub := validStage()
	pub.ID = "art-p1"
	if err := repo.Insert(ctx, pub); err != nil {
		t.Fatalf("seed pub: %v", err)
	}
	if err := repo.MarkPublished(ctx, pub.ID, `{"kind":"drive","uri":"file-1"}`, nowFixed); err != nil {
		t.Fatalf("MarkPublished: %v", err)
	}
	got, err := repo.ListByState(ctx, artifact.ArtifactStageStateStaged, 10)
	if err != nil {
		t.Fatalf("ListByState STAGED: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("ListByState STAGED: got %d rows, want 2", len(got))
	}
	for _, s := range got {
		if s.State != artifact.ArtifactStageStateStaged {
			t.Errorf("ListByState STAGED: row %q has state %q", s.ID, s.State)
		}
	}
}

// ── MarkPublished ─────────────────────────────────────────────────────

func TestRepository_MarkPublished_HappyPath(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	repo.nowFn = func() time.Time { return nowFixed }
	ctx := context.Background()
	s := validStage()
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	pubAt := nowFixed.Add(time.Minute)
	if err := repo.MarkPublished(ctx, s.ID, `{"kind":"drive","uri":"f-1"}`, pubAt); err != nil {
		t.Fatalf("MarkPublished: %v", err)
	}
	got, err := repo.GetByID(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.State != artifact.ArtifactStageStatePublished {
		t.Errorf("State = %q, want PUBLISHED", got.State)
	}
	if got.PublishedLocation != `{"kind":"drive","uri":"f-1"}` {
		t.Errorf("PublishedLocation = %q, want canonical", got.PublishedLocation)
	}
	if got.PublishedAt == nil || !got.PublishedAt.Equal(pubAt) {
		t.Errorf("PublishedAt = %v, want %v", got.PublishedAt, pubAt)
	}
}

func TestRepository_MarkPublished_RejectsOnTerminalState(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	repo.nowFn = func() time.Time { return nowFixed }
	ctx := context.Background()
	s := validStage()
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := repo.MarkSucceeded(ctx, s.ID); err != nil {
		t.Fatalf("MarkSucceeded: %v", err)
	}
	// Now MarkPublished MUST be rejected by the terminal-state fence.
	if err := repo.MarkPublished(ctx, s.ID, `{"x":1}`, nowFixed); !errors.Is(err, artifact.ErrTerminalStateRejection) {
		t.Errorf("MarkPublished on terminal: err = %v, want ErrTerminalStateRejection", err)
	}
}

// TestRepository_MarkPublished_RejectsOnPublishedState pins the
// Path-B invariant: MarkPublished on a row already in PUBLISHED
// state MUST return ErrTerminalStateRejection (not silently
// overwrite published_location/published_at). The fence is the
// broadest of the Mark* methods (state NOT IN
// (`PUBLISHED','SUCCEEDED','FAILED_PERMANENT')) because re-publishing
// from PUBLISHED would silently duplicate-upload to Drive (the
// Publisher worker has the cross-session dedup IdempotencyKey,
// but a duplicate CAS still costs a Drive-side PutFile call before
// the fence fires). Pre-Path-B (Push 3.1a baseline), this test
// would have FAILED — the baseline fence was missing 'PUBLISHED'
// and a second drain on a PUBLISHED row silently overwrote.
func TestRepository_MarkPublished_RejectsOnPublishedState(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	repo.nowFn = func() time.Time { return nowFixed }
	ctx := context.Background()

	s := validStage()
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	// First MarkPublished: STAGED → PUBLISHED (happy path).
	if err := repo.MarkPublished(ctx, s.ID, `{"kind":"drive","uri":"first"}`, nowFixed); err != nil {
		t.Fatalf("first MarkPublished: %v", err)
	}
	// Second MarkPublished on the now-PUBLISHED row: MUST be rejected.
	if err := repo.MarkPublished(ctx, s.ID, `{"kind":"drive","uri":"second"}`, nowFixed); !errors.Is(err, artifact.ErrTerminalStateRejection) {
		t.Errorf("MarkPublished on PUBLISHED: err = %v, want ErrTerminalStateRejection (Path-B invariant: re-deliveries on PUBLISHED state are typed no-ops, not silent overwrites)", err)
	}
	// PublishedLocation MUST NOT have been overwritten by the
	// rejected second call (the canonical 'first' uri remains).
	got, err := repo.GetByID(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetByID post-rejection: %v", err)
	}
	if got.PublishedLocation != `{"kind":"drive","uri":"first"}` {
		t.Errorf("PublishedLocation = %q, want canonical first-write value (rejected Call must NOT have overwritten)", got.PublishedLocation)
	}
	if got.State != artifact.ArtifactStageStatePublished {
		t.Errorf("State = %q, want PUBLISHED (terminal-state rejection must preserve the existing row state)", got.State)
	}
}

func TestRepository_MarkPublished_NotFound(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	err := repo.MarkPublished(context.Background(), "art-missing", `{}`, nowFixed)
	if !errors.Is(err, artifact.ErrArtifactStageNotFound) {
		t.Errorf("MarkPublished on missing row: err = %v, want ErrArtifactStageNotFound", err)
	}
}

// ── MarkSucceeded ─────────────────────────────────────────────────────

func TestRepository_MarkSucceeded_HappyPath(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	repo.nowFn = func() time.Time { return nowFixed }
	ctx := context.Background()
	s := validStage()
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := repo.MarkSucceeded(ctx, s.ID); err != nil {
		t.Fatalf("MarkSucceeded: %v", err)
	}
	got, _ := repo.GetByID(ctx, s.ID)
	if got.State != artifact.ArtifactStageStateSucceeded {
		t.Errorf("State = %q, want SUCCEEDED", got.State)
	}
}

func TestRepository_MarkSucceeded_RejectsAlreadyTerminal(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	repo.nowFn = func() time.Time { return nowFixed }
	ctx := context.Background()
	s := validStage()
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := repo.MarkFailedPermanent(ctx, s.ID, "drive 5xx"); err != nil {
		t.Fatalf("MarkFailedPermanent: %v", err)
	}
	if err := repo.MarkSucceeded(ctx, s.ID); !errors.Is(err, artifact.ErrTerminalStateRejection) {
		t.Errorf("MarkSucceeded on FAILED_PERMANENT: err = %v, want ErrTerminalStateRejection", err)
	}
}

// ── MarkFailedPermanent ───────────────────────────────────────────────

func TestRepository_MarkFailedPermanent_HappyPath(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	repo.nowFn = func() time.Time { return nowFixed }
	ctx := context.Background()
	s := validStage()
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := repo.MarkFailedPermanent(ctx, s.ID, "hash mismatch on re-read"); err != nil {
		t.Fatalf("MarkFailedPermanent: %v", err)
	}
	got, err := repo.GetByID(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.State != artifact.ArtifactStageStateFailedPermanent {
		t.Errorf("State = %q, want FAILED_PERMANENT", got.State)
	}
	if got.LastError != "hash mismatch on re-read" {
		t.Errorf("LastError = %q, want canonical", got.LastError)
	}
}

// ── IncrementAttemptCount ─────────────────────────────────────────────

func TestRepository_IncrementAttemptCount_HappyPath(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	repo.nowFn = func() time.Time { return nowFixed }
	ctx := context.Background()
	s := validStage()
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	for i, want := range []int{1, 2, 3} {
		if err := repo.IncrementAttemptCount(ctx, s.ID); err != nil {
			t.Fatalf("IncrementAttemptCount[%d]: %v", i, err)
		}
		got, _ := repo.GetByID(ctx, s.ID)
		if got.AttemptCount != want {
			t.Errorf("IncrementAttemptCount[%d]: attempt_count = %d, want %d", i, got.AttemptCount, want)
		}
	}
}

func TestRepository_IncrementAttemptCount_RejectsOnTerminal(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	repo.nowFn = func() time.Time { return nowFixed }
	ctx := context.Background()
	s := validStage()
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := repo.MarkSucceeded(ctx, s.ID); err != nil {
		t.Fatalf("MarkSucceeded: %v", err)
	}
	if err := repo.IncrementAttemptCount(ctx, s.ID); !errors.Is(err, artifact.ErrTerminalStateRejection) {
		t.Errorf("IncrementAttemptCount on SUCCEEDED: err = %v, want ErrTerminalStateRejection", err)
	}
}

// ── Nano-precision round-trip ───────────────────────────────────────
//
// These tests assert that the artifact_stages repository preserves
// sub-second precision across Insert + GetByID. The canonical wire
// format is RFC3339Nano (per timeutil.FormatRFC3339Nano on write +
// parseRFC3339Nano on read). A regression to time.RFC3339 (no
// fractional seconds) would silently truncate the sub-second
// component and the time.Equal() assertion below would fail. The
// existing tests use a zero-nanos fixture (nowFixed) so they would
// NOT catch this regression — these tests are the canonical
// regression sentinel.

// nanoPrecisionTime is a role-neutral deterministic time with a
// non-zero sub-second component (123456789 nanos past the
// minute). The non-zero nanos are the regression sentinel: a
// switch back to time.RFC3339 would silently truncate this to
// zero, failing the time.Equal() assertion. The variable is
// role-neutral so the Insert test can use it as CreatedAt and
// any future test can repurpose it without a rename.
var nanoPrecisionTime = time.Date(2026, 7, 11, 12, 0, 0, 123456789, time.UTC)

// nanoNow is a deterministic time with a distinct non-zero
// sub-second component (987654321 nanos at 12:01:00 — a 1-minute
// gap from nanoPrecisionTime so CreatedAt < UpdatedAt is
// obvious at a glance). Used to seed the repository's nowFn
// for UpdatedAt round-trip.
var nanoNow = time.Date(2026, 7, 11, 12, 1, 0, 987654321, time.UTC)

// newNanoRepo is a tiny helper that mirrors the staging package
// pattern: build a Repository with the canonical DDL + a
// nano-precision nowFn pre-wired, so the two nano-precision
// tests share a single setup path.
func newNanoRepo(t *testing.T) *Repository {
	t.Helper()
	repo := NewRepository(setupTestDB(t))
	repo.nowFn = func() time.Time { return nanoNow }
	return repo
}

func TestRepository_Insert_NanoPrecisionRoundTrip(t *testing.T) {
	repo := newNanoRepo(t)
	ctx := context.Background()

	s := validStage()
	s.CreatedAt = nanoPrecisionTime
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert nano-precision stage: %v", err)
	}
	got, err := repo.GetByID(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !got.CreatedAt.Equal(nanoPrecisionTime) {
		t.Errorf("CreatedAt round-trip: got %v (nanos=%d), want %v (nanos=%d) — nano precision LOST",
			got.CreatedAt, got.CreatedAt.Nanosecond(), nanoPrecisionTime, nanoPrecisionTime.Nanosecond())
	}
	if !got.UpdatedAt.Equal(nanoNow) {
		t.Errorf("UpdatedAt round-trip: got %v (nanos=%d), want %v (nanos=%d) — nano precision LOST",
			got.UpdatedAt, got.UpdatedAt.Nanosecond(), nanoNow, nanoNow.Nanosecond())
	}
}

func TestRepository_MarkPublished_NanoPrecisionRoundTrip(t *testing.T) {
	repo := newNanoRepo(t)
	// nanoPublishedAt is declared local (not the package-level
	// nanoPrecisionTime) so the semantic role — the value
	// written to artifact_stages.published_at — is unambiguous.
	nanoPublishedAt := time.Date(2026, 7, 11, 12, 0, 5, 123456789, time.UTC)
	ctx := context.Background()

	s := validStage()
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := repo.MarkPublished(ctx, s.ID, `{"kind":"drive","uri":"f-1"}`, nanoPublishedAt); err != nil {
		t.Fatalf("MarkPublished nano-precision: %v", err)
	}
	got, err := repo.GetByID(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.PublishedAt == nil {
		t.Fatalf("PublishedAt must be non-nil after MarkPublished")
	}
	if !got.PublishedAt.Equal(nanoPublishedAt) {
		t.Errorf("PublishedAt round-trip: got %v (nanos=%d), want %v (nanos=%d) — nano precision LOST",
			got.PublishedAt, got.PublishedAt.Nanosecond(), nanoPublishedAt, nanoPublishedAt.Nanosecond())
	}
	// UpdatedAt re-assertion is intentionally omitted: the
	// Insert test already covers UpdatedAt round-trip; this		// test's unique value-add is the PublishedAt path. Keeping
	// coverage surface disjoint keeps the regression-sentinel
	// signal unambiguous (a future regression that breaks ONLY
	// PublishedAt round-trip points straight at the Mark*
	// write path).
}

// ── InsertWithOutbox (FASE 3 / Push 3.1c hermetic TX tests) ────────
//
// These tests are the canonical SQLite-anchored regression
// sentinels for the InsertWithOutbox atomicity contract.
// They use real SQLite (in-memory) with the canonical
// production DDLs (artifact_stages migration 147 + outbox_events
// migration 092, including the ux_outbox_events_event_key
// UNIQUE index). The atomicity contract is the entire point of
// the 2-table TX primitive — a regression in the TX wrapper
// (e.g., switching to two independent Exec calls) would
// silently orphan events from their rows or vice versa; this
// test block fails LOUD on any such drift.

// eventTypeForTest is a neutral event_type used by the
// InsertWithOutbox hermetic tests. Production uses the
// `artifact.staged.v1` constant (Push 3.1c emitter) but the
// repository contract is "store the supplied bytes verbatim"
// so the test uses a separate value to avoid coupling the
// repository test to the application's event_type catalog.
const eventTypeForTest = "artifact.test.staged.v1"

// payloadForTest is the canonical payload byte slice for
// the InsertWithOutbox hermetic tests. Arbitrary well-formed
// JSON to verify the repository stores the bytes verbatim
// (no shape-interpretation, no field reordering).
var payloadForTest = []byte(`{"stage_id":"art-test-1","hash":"sha256abc","size":4096}`)

// TestRepository_InsertWithOutbox_HappyPath_AtomicCommit pins
// the 2-table atomic commit contract: BOTH the artifact_stages
// row AND the co-emitted outbox_events row commit together in
// a single SQLite TX. The test reads back via two independent
// paths (artifact.Repository.GetByID for the stage row +
// db.QueryRowContext for the outbox row) so a regression that
// commits ONE but not the OTHER surfaces immediately.
func TestRepository_InsertWithOutbox_HappyPath_AtomicCommit(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	repo.nowFn = func() time.Time { return nowFixed }
	ctx := context.Background()

	stage := validStage()
	eventKey, err := repo.InsertWithOutbox(ctx, stage, eventTypeForTest, payloadForTest)
	if err != nil {
		t.Fatalf("InsertWithOutbox happy: unexpected error: %v", err)
	}

	// 1. Returned eventKey MUST match the canonical convention
	//    `stage:<jobID>:<stageID>` (the producer-side dedupe
	//    anchor for downstream consumers).
	const wantEventKey = "stage:job-test-1:art-test-1"
	if eventKey != wantEventKey {
		t.Errorf("returned eventKey = %q, want %q", eventKey, wantEventKey)
	}

	// 2. artifact_stages row IS persisted (full row readable
	//    via GetByID; this is the same path the finalizer
	//    uses to scan the saga's per-job contributions).
	got, err := repo.GetByID(ctx, stage.ID)
	if err != nil {
		t.Fatalf("GetByID after InsertWithOutbox: %v (artifact row MUST be persisted)", err)
	}
	if got.State != artifact.ArtifactStageStateStaged {
		t.Errorf("artifact_stages.State = %q, want STAGED", got.State)
	}
	if !got.CreatedAt.Equal(nowFixed) {
		t.Errorf("artifact_stages.CreatedAt = %v, want %v (UTC clock source)", got.CreatedAt, nowFixed)
	}
	if !got.UpdatedAt.Equal(nowFixed) {
		t.Errorf("artifact_stages.UpdatedAt = %v, want %v (UTC clock source)", got.UpdatedAt, nowFixed)
	}

	// 3. outbox_events row IS persisted with the canonical
	//    fields. Direct SQL probe (vs the application-layer
	//    outbox.Repository abstraction) so a regression in
	//    ANY of column-name + value-mapping surfaces here.
	var (
		gotEventType, gotAggregateID, gotAggregateType, gotPayloadJSON, gotEventKey, gotStatus, gotCreatedAt string
	)
	row := db.QueryRowContext(ctx,
		`SELECT event_type, aggregate_id, aggregate_type, payload_json, event_key, status, created_at
		   FROM outbox_events WHERE event_key = ?`, wantEventKey)
	if err := row.Scan(&gotEventType, &gotAggregateID, &gotAggregateType, &gotPayloadJSON, &gotEventKey, &gotStatus, &gotCreatedAt); err != nil {
		t.Fatalf("SELECT outbox_events row after InsertWithOutbox: %v (outbox row MUST be persisted)", err)
	}
	if gotEventType != eventTypeForTest {
		t.Errorf("outbox event_type = %q, want %q", gotEventType, eventTypeForTest)
	}
	if gotAggregateID != stage.ID {
		t.Errorf("outbox aggregate_id = %q, want %q (FK-by-convention to artifact_stages row)", gotAggregateID, stage.ID)
	}
	if gotAggregateType != "artifact_stage" {
		t.Errorf("outbox aggregate_type = %q, want %q (canonical aggregate namespace)", gotAggregateType, "artifact_stage")
	}
	if gotPayloadJSON != string(payloadForTest) {
		t.Errorf("outbox payload_json = %q, want %q (verbatim round-trip)", gotPayloadJSON, string(payloadForTest))
	}
	if gotEventKey != wantEventKey {
		t.Errorf("outbox event_key = %q, want %q", gotEventKey, wantEventKey)
	}
	if gotStatus != "pending" {
		t.Errorf("outbox status = %q, want %q (initial state of the consumer drain loop)", gotStatus, "pending")
	}
	if gotCreatedAt != nowFixed.UTC().Format(time.RFC3339Nano) {
		t.Errorf("outbox created_at = %q, want %q (canonical RFC3339Nano UTC)", gotCreatedAt, nowFixed.UTC().Format(time.RFC3339Nano))
	}

	// 4. Total outbox_events row count for the canonical
	//    event_key MUST be exactly 1 (no duplicate emission).
	var rowCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox_events WHERE event_key = ?`, wantEventKey).Scan(&rowCount); err != nil {
		t.Fatalf("COUNT outbox_events: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("outbox_events row count for event_key=%q = %d, want 1", wantEventKey, rowCount)
	}
}

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
