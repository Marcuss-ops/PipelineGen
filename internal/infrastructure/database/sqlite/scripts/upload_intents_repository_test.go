// Package scripts — upload_intents_repository_test.go (audit P0 #4 commit A/2, July 2026).
//
// TDD coverage for the canonical upload_intents concrete. Uses
// in-memory SQLite (per existing scripts_test.go pattern via
// sql.Open("sqlite3", ":memory:") + the `_ "github.com/mattn/go-sqlite3"`
// driver import — AGENTS.md database lock forbids any other driver).
package scripts

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// ─── Test scaffold ─────────────────────────────────────────────────────────

const uploadIntentsTestSchema = `
	CREATE TABLE upload_intents (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		voiceover_id    TEXT    NOT NULL UNIQUE,
		drive_file_id   TEXT    NOT NULL DEFAULT '',
		status          TEXT    NOT NULL CHECK (status IN ('pending','uploaded','finalized','completed','failed')),
		reason          TEXT    NOT NULL DEFAULT '',
		attempts        INTEGER NOT NULL DEFAULT 0,
		created_at      INTEGER NOT NULL,
		updated_at      INTEGER NOT NULL
	);
	CREATE INDEX idx_upload_intents_status_updated_at ON upload_intents (status, updated_at);
	CREATE INDEX idx_upload_intents_voiceover_id       ON upload_intents (voiceover_id);
`

func newUploadIntentTestRepo(t *testing.T) (*UploadIntentsRepository, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open :memory: sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(uploadIntentsTestSchema); err != nil {
		t.Fatalf("apply test schema: %v", err)
	}
	return NewUploadIntentsRepository(db), db
}

// mustInsert inserts a row in pending with a synthetic timestamp path.
// Tests that want to exercise ListPending's age-filter must call
// `backdateUpdatedAt` after this returns.
func mustInsert(t *testing.T, repo *UploadIntentsRepository, voiceoverID string) int64 {
	t.Helper()
	ctx := context.Background()
	tx, err := repo.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	id, err := repo.InsertTx(ctx, tx, &InsertUploadIntentOptions{VoiceoverID: voiceoverID})
	if err != nil {
		t.Fatalf("InsertTx(%s): %v", voiceoverID, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("tx.Commit: %v", err)
	}
	return id
}

func mustReadRow(t *testing.T, db *sql.DB, voiceoverID string) (status, driveFileID, reason string, attempts int) {
	t.Helper()
	if err := db.QueryRow(
		`SELECT status, drive_file_id, reason, attempts FROM upload_intents WHERE voiceover_id = ?`,
		voiceoverID,
	).Scan(&status, &driveFileID, &reason, &attempts); err != nil {
		t.Fatalf("readback %s: %v", voiceoverID, err)
	}
	return
}

// backdateUpdatedAt rewinds the row's updated_at so ListPending's
// time filter can pick it up at `now`.
func backdateUpdatedAt(t *testing.T, db *sql.DB, voiceoverID string, secsAgo int) {
	t.Helper()
	if _, err := db.Exec(
		`UPDATE upload_intents SET updated_at = ? WHERE voiceover_id = ?`,
		time.Now().Unix()-int64(secsAgo), voiceoverID,
	); err != nil {
		t.Fatalf("backdate %s: %v", voiceoverID, err)
	}
}

// ─── Tests ────────────────────────────────────────────────────────────────

// Round-trip Insert + ListPending: insert two rows, backdate one,
// confirm ListPending returns only the backdated one in updated_at ASC
// order. This is the canonical happy-path for the orphan sweeper (commit
// B/2) on the production-read path.
func TestUploadIntentsRepository_RoundTripAndListPending(t *testing.T) {
	repo, db := newUploadIntentTestRepo(t)
	ctx := context.Background()

	mustInsert(t, repo, "vo-fresh")
	mustInsert(t, repo, "vo-stale")
	backdateUpdatedAt(t, db, "vo-stale", 600) // 10 minutes old

	out, err := repo.ListPending(ctx, time.Now().Add(-5*time.Minute))
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 stale row, got %d: %+v", len(out), out)
	}
	if out[0].VoiceoverID != "vo-stale" {
		t.Errorf("expected vo-stale first (ASC updated_at), got %s", out[0].VoiceoverID)
	}
	if out[0].Status != "pending" {
		t.Errorf("expected pending status, got %q", out[0].Status)
	}
	if out[0].Status == "" || out[0].VoiceoverID == "" {
		t.Error("row fields must not be empty (debug: prior str-typing regression tripwire)")
	}
}

// Status enum Check constraint: inserting a row with status bypassed
// (via raw SQL) into the wrong status must fail (the SQLite CHECK clause
// is the source-of-truth gating for callers trying to short-circuit
// the state machine). Confirms godlike/07 single-closed-enum rule.
func TestUploadIntentsRepository_StatusEnumRejected(t *testing.T) {
	_, db := newUploadIntentTestRepo(t)
	_, err := db.Exec(`
		INSERT INTO upload_intents (voiceover_id, status, attempts, created_at, updated_at)
		VALUES ('vo-bad', 'bogus_status', 0, 0, 0)`)
	if err == nil {
		t.Fatal("expected CHECK constraint failure on bogus status, got nil")
	}
	// Verify the row did NOT land (godlike/07 no fake availability):
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM upload_intents WHERE voiceover_id = 'vo-bad'`).Scan(&n); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if n != 0 {
		t.Errorf("bogus-status row should not have been inserted; got count=%d", n)
	}
}

// MarkUploaded happy path: insert + MarkUploaded → row at 'uploaded' +
// drive_file_id stamped + attempts bumped to 1.
func TestUploadIntentsRepository_MarkUploadedHappyPath(t *testing.T) {
	repo, _ := newUploadIntentTestRepo(t)
	ctx := context.Background()

	mustInsert(t, repo, "vo-1")
	if err := repo.MarkUploaded(ctx, "vo-1", "drive-file-xyz"); err != nil {
		t.Fatalf("MarkUploaded: %v", err)
	}
	status, driveFileID, _, attempts := mustReadRow(t, repo.db, "vo-1")
	if status != "uploaded" {
		t.Errorf("expected status 'uploaded', got %q", status)
	}
	if driveFileID != "drive-file-xyz" {
		t.Errorf("expected drive_file_id stamped, got %q", driveFileID)
	}
	if attempts != 1 {
		t.Errorf("expected attempts=1 (bumped on Mark), got %d", attempts)
	}
}

// MarkUploaded WHERE filter: MarkUploaded against a row that is NOT at
// 'pending' (e.g. already 'uploaded' or 'completed') returns
// ErrUploadIntentNotFound. This is the typed-no-fake-availability path
// (godlike/07).
func TestUploadIntentsRepository_MarkUploadedFilterBlocksNonPending(t *testing.T) {
	repo, _ := newUploadIntentTestRepo(t)
	ctx := context.Background()

	mustInsert(t, repo, "vo-1")
	if err := repo.MarkUploaded(ctx, "vo-1", "drive-1"); err != nil {
		t.Fatalf("first MarkUploaded: %v", err)
	}
	// Second MarkUploaded against the already-uploaded row: UPDATEs
	// WHERE status='pending' yield 0 rows; ErrUploadIntentNotFound
	// surfaces per the contract.
	err := repo.MarkUploaded(ctx, "vo-1", "drive-2")
	if err == nil {
		t.Fatal("expected ErrUploadIntentNotFound on second MarkUploaded, got nil")
	}
	if !errors.Is(err, ErrUploadIntentNotFound) {
		t.Errorf("expected wrap of ErrUploadIntentNotFound, got: %v", err)
	}
}

// Missing voiceover_id: Mark* method surfaces ErrUploadIntentNotFound
// (NOT silent nil — godlike/07 typed-failure).
func TestUploadIntentsRepository_MarkMissingReturnsTypedSentinel(t *testing.T) {
	repo, _ := newUploadIntentTestRepo(t)
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"MarkUploaded", func() error { return repo.MarkUploaded(ctx, "vo-none", "d") }},
		{"MarkFinalized", func() error { return repo.MarkFinalized(ctx, "vo-none") }},
		{"MarkCompleted", func() error { return repo.MarkCompleted(ctx, "vo-none") }},
		{"MarkFailed", func() error { return repo.MarkFailed(ctx, "vo-none", "r") }},
	} {
		err := tc.call()
		if err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
			continue
		}
		if !errors.Is(err, ErrUploadIntentNotFound) {
			t.Errorf("%s: expected ErrUploadIntentNotFound, got: %v", tc.name, err)
		}
		if !strings.Contains(err.Error(), "voiceover_id=vo-none") {
			t.Errorf("%s: expected error message to mention voiceover_id, got: %v", tc.name, err)
		}
	}
}

// Five-step state machine: pending → uploaded → finalized → completed
// must succeed end-to-end, AND the row must be at 'completed' with no
// failures stamped.
func TestUploadIntentsRepository_FiveStepStateMachine(t *testing.T) {
	repo, _ := newUploadIntentTestRepo(t)
	ctx := context.Background()

	mustInsert(t, repo, "vo-flow")
	if err := repo.MarkUploaded(ctx, "vo-flow", "drive-1"); err != nil {
		t.Fatalf("MarkUploaded: %v", err)
	}
	if err := repo.MarkFinalized(ctx, "vo-flow"); err != nil {
		t.Fatalf("MarkFinalized: %v", err)
	}
	if err := repo.MarkCompleted(ctx, "vo-flow"); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}

	status, driveFileID, _, attempts := mustReadRow(t, repo.db, "vo-flow")
	if status != "completed" {
		t.Errorf("expected status 'completed', got %q", status)
	}
	if driveFileID != "drive-1" {
		t.Errorf("drive_file_id must persist across all 3 marks, got %q", driveFileID)
	}
	if attempts != 3 {
		t.Errorf("expected attempts=3 (one bump per Mark), got %d", attempts)
	}
}

// MarkFailed on a 'pending' row transitions it to 'failed', stamping
// the explicit reason and bumping attempts.
func TestUploadIntentsRepository_MarkFailedStampsReasonAndBumpsAttempts(t *testing.T) {
	repo, _ := newUploadIntentTestRepo(t)
	ctx := context.Background()

	mustInsert(t, repo, "vo-fail")
	if err := repo.MarkFailed(ctx, "vo-fail", "test_reason: simulated drive 503"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	status, _, reason, attempts := mustReadRow(t, repo.db, "vo-fail")
	if status != "failed" {
		t.Errorf("expected 'failed', got %q", status)
	}
	if reason != "test_reason: simulated drive 503" {
		t.Errorf("expected reason stamped, got %q", reason)
	}
	if attempts != 1 {
		t.Errorf("expected attempts=1, got %d", attempts)
	}

	// Empty-reason fallback MUST still set a non-empty default
	// reason (godlike/07 no-NULL-trap-on-log-grep invariant).
	mustInsert(t, repo, "vo-fail-empty")
	if err := repo.MarkFailed(ctx, "vo-fail-empty", ""); err != nil {
		t.Fatalf("MarkFailed empty reason: %v", err)
	}
	status, _, reason, _ = mustReadRow(t, repo.db, "vo-fail-empty")
	if status != "failed" {
		t.Errorf("expected 'failed', got %q", status)
	}
	if reason == "" {
		t.Error("expected defaulted reason ('unspecified_reason'), got empty (NULL-trap risk)")
	}
}

// InsertTx UNIQUE(voiceover_id) constraint: a second InsertTx with the
// same voiceover_id surfaces sql.ErrConstraintFailed (via %w wrap).
// This is the use case's idempotency-detection surface — UNIQUE
// violation = "row already exists, proceed with Mark*".
func TestUploadIntentsRepository_InsertTxUniqueConstraint(t *testing.T) {
	repo, _ := newUploadIntentTestRepo(t)
	ctx := context.Background()

	mustInsert(t, repo, "vo-dup")

	tx, err := repo.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = repo.InsertTx(ctx, tx, &InsertUploadIntentOptions{VoiceoverID: "vo-dup"})
	if err == nil {
		t.Fatal("expected constraint violation on duplicate voiceover_id, got nil")
	}
	if !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Errorf("expected UNIQUE constraint failed chain (driver text), got: %v", err)
	}
}

// InsertTx correctness guards (godlike/05 wiring-error fail-fast):
// nil tx / nil opts / empty VoiceoverID all surface typed errors,
// never panic.
func TestUploadIntentsRepository_InsertTxWiringGuards(t *testing.T) {
	repo, _ := newUploadIntentTestRepo(t)
	ctx := context.Background()
	tx, err := repo.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := repo.InsertTx(ctx, nil, &InsertUploadIntentOptions{VoiceoverID: "v"}); err == nil {
		t.Error("expected nil-tx error")
	}
	if _, err := repo.InsertTx(ctx, tx, nil); err == nil {
		t.Error("expected nil-opts error")
	}
	if _, err := repo.InsertTx(ctx, tx, &InsertUploadIntentOptions{VoiceoverID: ""}); err == nil {
		t.Error("expected empty-VoiceoverID error")
	}
}
