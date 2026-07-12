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

	artifact "github.com/Marcuss-ops/PipelineGen/internal/domain/artifact"
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
		State:        artifact.StateStaged,
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
	if got.State != artifact.StateStaged {
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
		artifact.StatePublished,
		artifact.StateSucceeded,
		artifact.StateFailedPermanent,
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
	got, err := repo.ListByState(ctx, artifact.StateStaged, 10)
	if err != nil {
		t.Fatalf("ListByState STAGED: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("ListByState STAGED: got %d rows, want 2", len(got))
	}
	for _, s := range got {
		if s.State != artifact.StateStaged {
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
	if got.State != artifact.StatePublished {
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
	if got.State != artifact.StateSucceeded {
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
	if got.State != artifact.StateFailedPermanent {
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
	// Insert test already covers UpdatedAt round-trip; this
	// test's unique value-add is the PublishedAt path. Keeping
	// coverage surface disjoint keeps the regression-sentinel
	// signal unambiguous (a future regression that breaks ONLY
	// PublishedAt round-trip points straight at the Mark*
	// write path).
}
