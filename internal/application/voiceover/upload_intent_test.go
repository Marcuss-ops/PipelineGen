// Package voiceover — upload_intent_test.go (audit P0 #4 commit A/2, July 2026).
//
// TDD coverage for UploadIntentUseCase.Execute with mock ports.
// The contract under test is the 5-step lifecycle with fail-closed
// MarkFailed emission (godlike/07 NO_FAKE_AVAILABILITY). The
// canonical hard requirement: Step 2 (Drive.Upload) OK + Step 4
// (Finalize) FAIL → row MUST remain at 'uploaded' (NOT 'completed')
// and MarkFailed MUST have been called exactly once with reason
// `finalize_failed: <cause>`. This is the user-spec contract for
// the orphan sweeper (commit B/2) to detect via ListPending.
//
// P2.6 migration (July 2026, retirement of the pre-deprecation
// voiceover.DriveUploaderAdapter port): the mock port surface is
// now `*mockPublisher` (satisfying canonical `delivery.Publisher`),
// and the `UploadIntentDeps` field is renamed from `DriveUploader`
// to `Publisher`. Step 2 in the production use case now routes
// through `delivery.Publisher.Publish(DestinationVoiceover,
// ParentFolderID=folderID, …)` per DRIVE-CUTOVER-P0-1 closure.
package voiceover

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// ─── Mock ports ───────────────────────────────────────────────────────────

type mockUploadIntentRepo struct {
	mu sync.Mutex
	db *sql.DB

	// Cerecorded method invocations
	startedTx       int
	inserted        int
	markedUploaded  []string // voiceover_id on each call
	markedFinalized []string
	markedCompleted []string
	markedFailed    []struct{ VoiceoverID, Reason string }

	// Forced outcomes (per-method error injection)
	insertErr        error
	markUploadedErr  error
	markFinalizedErr error
	markCompletedErr error
	markFailedErr    error
}

func newMockRepoWithDB(t *testing.T) *mockUploadIntentRepo {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open :memory: sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE upload_intents (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			voiceover_id    TEXT    NOT NULL UNIQUE,
			drive_file_id   TEXT    NOT NULL DEFAULT '',
			status          TEXT    NOT NULL CHECK (status IN ('pending','uploaded','finalized','completed','failed')),
			reason          TEXT    NOT NULL DEFAULT '',
			attempts        INTEGER NOT NULL DEFAULT 0,
			created_at      INTEGER NOT NULL,
			updated_at      INTEGER NOT NULL
		)`); err != nil {
		t.Fatalf("create upload_intents table: %v", err)
	}
	return &mockUploadIntentRepo{db: db}
}

func (m *mockUploadIntentRepo) BeginTx(ctx context.Context) (*sql.Tx, error) {
	m.mu.Lock()
	m.startedTx++
	m.mu.Unlock()
	return m.db.BeginTx(ctx, nil)
}

func (m *mockUploadIntentRepo) InsertTx(ctx context.Context, tx *sql.Tx, opts *UploadIntentInsertOptions) (int64, error) {
	m.mu.Lock()
	if m.insertErr != nil {
		m.mu.Unlock()
		return 0, m.insertErr
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO upload_intents (voiceover_id, status, attempts, created_at, updated_at)
		VALUES (?, 'pending', 0, ?, ?)`,
		opts.VoiceoverID, time.Now().Unix(), time.Now().Unix())
	if err != nil {
		m.mu.Unlock()
		return 0, err
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT last_insert_rowid()`).Scan(&id); err != nil {
		m.mu.Unlock()
		return 0, err
	}
	m.inserted++
	m.mu.Unlock()
	return id, nil
}

func (m *mockUploadIntentRepo) MarkUploaded(ctx context.Context, voiceoverID, driveFileID string) error {
	m.mu.Lock()
	m.markedUploaded = append(m.markedUploaded, voiceoverID)
	m.mu.Unlock()
	if m.markUploadedErr != nil {
		return m.markUploadedErr
	}
	res, err := m.db.ExecContext(ctx, `
		UPDATE upload_intents SET status = 'uploaded', drive_file_id = ?, attempts = attempts + 1, updated_at = ?
		WHERE voiceover_id = ? AND status = 'pending'`,
		driveFileID, time.Now().Unix(), voiceoverID)
	if err != nil {
		return err
	}
	return typedNotFoundIfZeroRows(res, "uploaded", voiceoverID)
}

func (m *mockUploadIntentRepo) MarkFinalized(ctx context.Context, voiceoverID string) error {
	m.mu.Lock()
	m.markedFinalized = append(m.markedFinalized, voiceoverID)
	m.mu.Unlock()
	if m.markFinalizedErr != nil {
		return m.markFinalizedErr
	}
	res, err := m.db.ExecContext(ctx, `
		UPDATE upload_intents SET status = 'finalized', attempts = attempts + 1, updated_at = ?
		WHERE voiceover_id = ? AND status = 'uploaded'`,
		time.Now().Unix(), voiceoverID)
	if err != nil {
		return err
	}
	return typedNotFoundIfZeroRows(res, "finalized", voiceoverID)
}

func (m *mockUploadIntentRepo) MarkCompleted(ctx context.Context, voiceoverID string) error {
	m.mu.Lock()
	m.markedCompleted = append(m.markedCompleted, voiceoverID)
	m.mu.Unlock()
	if m.markCompletedErr != nil {
		return m.markCompletedErr
	}
	res, err := m.db.ExecContext(ctx, `
		UPDATE upload_intents SET status = 'completed', attempts = attempts + 1, updated_at = ?
		WHERE voiceover_id = ? AND status = 'finalized'`,
		time.Now().Unix(), voiceoverID)
	if err != nil {
		return err
	}
	return typedNotFoundIfZeroRows(res, "completed", voiceoverID)
}

func (m *mockUploadIntentRepo) MarkFailed(ctx context.Context, voiceoverID, reason string) error {
	m.mu.Lock()
	m.markedFailed = append(m.markedFailed, struct{ VoiceoverID, Reason string }{voiceoverID, reason})
	m.mu.Unlock()
	if reason == "" {
		reason = "unspecified_reason"
	}
	if m.markFailedErr != nil {
		return m.markFailedErr
	}
	res, err := m.db.ExecContext(ctx, `
		UPDATE upload_intents SET status = 'failed', reason = ?, attempts = attempts + 1, updated_at = ?
		WHERE voiceover_id = ? AND status IN ('pending','uploaded','finalized')`,
		reason, time.Now().Unix(), voiceoverID)
	if err != nil {
		return err
	}
	return typedNotFoundIfZeroRows(res, "failed", voiceoverID)
}

func (m *mockUploadIntentRepo) ListPending(ctx context.Context, olderThan time.Time) ([]UploadIntent, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, voiceover_id, drive_file_id, status, reason, attempts, updated_at
		FROM upload_intents
		WHERE (status = 'pending' OR status = 'uploaded') AND updated_at < ?`,
		olderThan.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UploadIntent
	for rows.Next() {
		var r UploadIntent
		if err := rows.Scan(&r.ID, &r.VoiceoverID, &r.DriveFileID, &r.Status, &r.Reason, &r.Attempts, &r.UpdatedUnix); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// typedNotFoundIfZeroRows is the mock-repo's faithful mirror of the
// production concrete's behavioural contract (see
// scripts.upload_intents_repository.updateStatusByVoiceoverID): a
// 0-rows-affected UPDATE returns an error wrapping
// scripts.ErrUploadIntentNotFound. Without this, the use case's
// errors.Is(err, scripts.ErrUploadIntentNotFound) branch would
// never fire in test even though it fires in production, masking
// real behaviour differences (e.g. orphan sweeper's
// re-discovery-on-row-absent path). The wrap format mirrors the
// concrete: `fmt.Errorf("%w (target=..., voiceover_id=...)", ErrUploadIntentNotFound, ...)`
// so errors.Is works AND the message is grep-able by
// scripts.ErrUploadIntentNotFound.Error() substring.
func typedNotFoundIfZeroRows(res sql.Result, target, voiceoverID string) error {
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("mock repo: RowsAffected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("%w (target=%s, voiceover_id=%s)",
			ErrUploadIntentNotFound, target, voiceoverID)
	}
	return nil
}

// Compile-time assertion (AGENTS.md Pattern 0): the mock publisher
// satisfies the canonical delivery.Publisher port. Drift surfaces
// as vet error.
var _ delivery.Publisher = (*mockPublisher)(nil)

// mockPublisher is the P2.6 replacement for mockDriveUploader.
// Satisfies canonical `delivery.Publisher` (Publish + ResolveFolder).
// ResolveFolder is a stub (return ("", nil)) — the use case's
// happy-path + failure scenarios only exercise Publish.
type mockPublisher struct {
	cannedFileID string
	cannedErr    error
	calls        int
}

func (m *mockPublisher) Publish(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	m.calls++
	if m.cannedErr != nil {
		return nil, m.cannedErr
	}
	return &delivery.PublishResult{FileID: m.cannedFileID}, nil
}

func (m *mockPublisher) ResolveFolder(ctx context.Context, req delivery.PublishRequest) (string, error) {
	// Tests do not exercise ResolveFolder; minimal stub.
	return "", nil
}

type mockFinalizer struct {
	cannedErr error
	calls     []string // voiceover_id on each call
}

func (m *mockFinalizer) Finalize(ctx context.Context, voiceoverID, driveFileID string) error {
	m.calls = append(m.calls, voiceoverID)
	return m.cannedErr
}

func readIntentStatus(t *testing.T, repo *mockUploadIntentRepo, voiceoverID string) (status, reason string) {
	t.Helper()
	if err := repo.db.QueryRow(`SELECT status, reason FROM upload_intents WHERE voiceover_id = ?`,
		voiceoverID).Scan(&status, &reason); err != nil {
		t.Fatalf("readback %s: %v", voiceoverID, err)
	}
	return
}

// ─── Tests ────────────────────────────────────────────────────────────────

// Compile-time assertion (AGENTS.md Pattern 0): the mock repo
// satisfies the application-layer port. Drift surfaces as vet error.
var _ UploadIntentsRepository = (*mockUploadIntentRepo)(nil)

// Happy-path: Drive OK + Finalize OK → row lands at 'completed' after
// all 5 steps; Drive + Finalize each called exactly once.
func TestUploadIntentUseCase_HappyPath(t *testing.T) {
	repo := newMockRepoWithDB(t)
	drive := &mockPublisher{cannedFileID: "drive-abc"}
	finalizer := &mockFinalizer{}
	uc := NewUploadIntentUseCase(UploadIntentDeps{
		Repo:             repo,
		Publisher:        drive,
		ProjectFinalizer: finalizer,
		Logger:           zap.NewNop(),
	})

	got, err := uc.Execute(context.Background(), "vo-happy", "/tmp/in.mp3", "in.mp3", "folder-x")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got != "drive-abc" {
		t.Errorf("expected returned driveFileID 'drive-abc', got %q", got)
	}
	status, _ := readIntentStatus(t, repo, "vo-happy")
	if status != "completed" {
		t.Errorf("expected row at 'completed', got %q", status)
	}
	if drive.calls != 1 {
		t.Errorf("Drive.UploadFile should be called once, got %d", drive.calls)
	}
	if len(finalizer.calls) != 1 {
		t.Errorf("Finalizer.Finalize should be called once, got %d", len(finalizer.calls))
	}
	if len(repo.markedFailed) != 0 {
		t.Errorf("no MarkFailed expected on happy path, got %d", len(repo.markedFailed))
	}
}

// ─── USER-SPEC CRITICAL TEST ───
// Drive upload OK + Finalize FAIL: row must stay at 'uploaded'
// (NOT 'completed'). MarkFailed MUST NOT be called on this path:
// firing MarkFailed would transition row to 'failed', removing it
// from the orphan sweeper's ListPending scan (which filters
// `WHERE status='pending' OR status='uploaded'`). The user-spec
// literal contract is "row resta in `uploaded` non `completed`",
// which is what this test pins. Drive.Upload MUST have been called
// (the orphan row corresponds to a Drive file that exists), and
// drive_file_id MUST be stamped so the sweeper can match + cleanup.
func TestUploadIntentUseCase_FinalizeFailure_LeavesRowAtUploaded(t *testing.T) {
	repo := newMockRepoWithDB(t)
	drive := &mockPublisher{cannedFileID: "drive-stuck"}
	finalizer := &mockFinalizer{cannedErr: errors.New("finalize db connection lost")}
	uc := NewUploadIntentUseCase(UploadIntentDeps{
		Repo:             repo,
		Publisher:        drive,
		ProjectFinalizer: finalizer,
		Logger:           zap.NewNop(),
	})

	got, err := uc.Execute(context.Background(), "vo-orphan", "/tmp/in.mp3", "in.mp3", "folder-x")
	if err == nil {
		t.Fatal("expected error from Execute when Finalize fails, got nil")
	}
	if got != "" {
		t.Errorf("expected empty driveFileID on partial-failure return, got %q", got)
	}
	if !strings.Contains(err.Error(), "ProjectFinalizer") {
		t.Errorf("expected wrapped ProjectFinalizer error, got: %v", err)
	}

	// ── CRITICAL ASSERTION ──
	status, reason := readIntentStatus(t, repo, "vo-orphan")
	if status != "uploaded" {
		t.Errorf("USER-SPEC REQUIREMENT: row MUST stay at 'uploaded' (NOT 'completed'), got %q\n"+
			"Orphan sweeper (commit B/2) ListPending filter is `WHERE status='pending' OR status='uploaded'`; "+
			"deviating from 'uploaded' makes the orphan unrecoverable.", status)
	}
	if status == "completed" {
		t.Error("USER-SPEC BUG: row at 'completed' despite Finalize failure — orphan sweeper cannot detect cleanup needed!")
	}
	if len(repo.markedFailed) != 0 {
		t.Errorf("USER-SPEC REQUIREMENT: MarkFailed MUST NOT fire on Finalize-FAIL (would push row to 'failed', "+
			"hiding it from orphan sweeper); got %d MarkFailed calls", len(repo.markedFailed))
	}
	if reason != "" {
		t.Errorf("expected reason column empty (MarkFailed not fired), got %q", reason)
	}
	if len(repo.markedFinalized) != 0 {
		t.Errorf("MarkFinalized must NOT be invoked after Finalize fail, got %d calls", len(repo.markedFinalized))
	}
	if len(repo.markedCompleted) != 0 {
		t.Errorf("MarkCompleted must NOT be invoked after Finalize fail, got %d calls", len(repo.markedCompleted))
	}
	if drive.calls != 1 {
		t.Errorf("Drive.UploadFile expected = 1 (orphan row corresponds to a real Drive upload), got %d", drive.calls)
	}
	var stampedFileID string
	if err := repo.db.QueryRow(
		`SELECT drive_file_id FROM upload_intents WHERE voiceover_id = ?`, "vo-orphan",
	).Scan(&stampedFileID); err != nil {
		t.Fatalf("read drive_file_id: %v", err)
	}
	if stampedFileID != "drive-stuck" {
		t.Errorf("orphan sweeper needs drive_file_id stamped; got %q", stampedFileID)
	}
}

// ─── USER-SPEC CRITICAL TEST (Step 4.5 variant) ───
// Drive upload OK + Finalize OK + MarkFinalized FAIL: row MUST stay
// at 'uploaded' (NOT 'failed' AND NOT 'completed'). The orphan
// sweeper (B/2) ListPending filter is `WHERE status='pending' OR
// status='uploaded'`; emitting MarkFailed on MarkFinalized-FAIL
// would push the row to 'failed', hiding it from the sweeper and
// leaving the Drive file unrecovered. MarkFailed MUST NOT fire on
// this path (parallels TestUploadIntentUseCase_FinalizeFailure_LeavesRowAtUploaded).
// Finalize-port + MarkCompleted must NOT have been called after
// MarkFinalized-fail.
func TestUploadIntentUseCase_MarkFinalizedFailure_LeavesRowAtUploaded(t *testing.T) {
	repo := newMockRepoWithDB(t)
	drive := &mockPublisher{cannedFileID: "drive-mffail"}
	finalizer := &mockFinalizer{}
	repo.markFinalizedErr = errors.New("sqlite database is locked (transient)")
	uc := NewUploadIntentUseCase(UploadIntentDeps{
		Repo:             repo,
		Publisher:        drive,
		ProjectFinalizer: finalizer,
		Logger:           zap.NewNop(),
	})

	got, err := uc.Execute(context.Background(), "vo-mffail", "/tmp/in.mp3", "in.mp3", "folder-x")
	if err == nil {
		t.Fatal("expected error from Execute when MarkFinalized fails, got nil")
	}
	if got != "" {
		t.Errorf("expected empty driveFileID on partial-failure return, got %q", got)
	}
	if !strings.Contains(err.Error(), "MarkFinalized") {
		t.Errorf("expected wrapped MarkFinalized error, got: %v", err)
	}

	// ── CRITICAL ASSERTION ──
	status, reason := readIntentStatus(t, repo, "vo-mffail")
	if status != "uploaded" {
		t.Errorf("USER-SPEC REQUIREMENT: row MUST stay at 'uploaded' (NOT 'failed', NOT 'completed'); got %q.\n"+
			"Orphan sweeper (B/2) ListPending filter is `WHERE status='pending' OR status='uploaded'`; "+
			"deviating from 'uploaded' makes the row invisible to sweeper-driven recovery.", status)
	}
	if len(repo.markedFailed) != 0 {
		t.Errorf("USER-SPEC REQUIREMENT: MarkFailed MUST NOT fire on MarkFinalized-FAIL (would push row to 'failed', "+
			"hiding it from orphan sweeper); got %d MarkFailed calls: %+v",
			len(repo.markedFailed), repo.markedFailed)
	}
	if reason != "" {
		t.Errorf("expected reason column empty (MarkFailed not fired), got %q", reason)
	}
	if drive.calls != 1 {
		t.Errorf("Drive.UploadFile expected = 1 (orphan row corresponds to a real Drive upload), got %d", drive.calls)
	}
	if len(finalizer.calls) != 1 {
		t.Errorf("Finalizer.Finalize expected = 1 (Step 4 before MarkFinalized), got %d", len(finalizer.calls))
	}
	if len(repo.markedFinalized) != 1 {
		t.Errorf("MarkFinalized expected = 1 (the failing call), got %d", len(repo.markedFinalized))
	}
	if len(repo.markedCompleted) != 0 {
		t.Errorf("MarkCompleted must NOT be invoked after MarkFinalized-fail, got %d calls", len(repo.markedCompleted))
	}
	var stampedFileID string
	if err := repo.db.QueryRow(
		`SELECT drive_file_id FROM upload_intents WHERE voiceover_id = ?`, "vo-mffail",
	).Scan(&stampedFileID); err != nil {
		t.Fatalf("read drive_file_id: %v", err)
	}
	if stampedFileID != "drive-mffail" {
		t.Errorf("orphan sweeper needs drive_file_id stamped; got %q", stampedFileID)
	}
}

// ─── USER-SPEC CRITICAL TEST (Step 5 lock — the Step 4.5 asymmetry) ───
// Drive upload OK + Finalize OK + MarkFinalized OK + MarkCompleted FAIL:
// row MUST be at 'failed' with reason 'mark_completed_failed: ...'.
//
// This test is the asymmetric counterpart to
// TestUploadIntentUseCase_MarkFinalizedFailure_LeavesRowAtUploaded:
// Step 4.5 leaves the row at 'uploaded' so the orphan sweeper (B/2)
// can detect + retry MarkFinalized. Step 5 is the FINAL lifecycle
// stamp; leaving the row at 'finalized' (without MarkFailed) would
// hide it from BOTH the orphan sweeper ('finalized' NOT in
// `WHERE status='pending' OR status='uploaded'` filter) AND operator
// visibility (no `reason` column on success transitions). The
// deliberate MarkFailed emission on Step 5 is a godlike/07
// operator-visibility contract — NOT a bug or oversight.
//
// Locking this protects against a future maintainer reflexively
// "cleaning up" Step 5 the same way as Step 4.5 (which would violate
// the user-spec hard requirement: "NON ritornare success se step 5
// fallisce senza lasciare MarkFailed"). The asymmetry is
// necessary; both tests must remain.
func TestUploadIntentUseCase_MarkCompletedFailure_LeavesRowAtFailed(t *testing.T) {
	repo := newMockRepoWithDB(t)
	drive := &mockPublisher{cannedFileID: "drive-mcfail"}
	finalizer := &mockFinalizer{}
	repo.markCompletedErr = errors.New("sqlite database is locked (transient)")
	uc := NewUploadIntentUseCase(UploadIntentDeps{
		Repo:             repo,
		Publisher:        drive,
		ProjectFinalizer: finalizer,
		Logger:           zap.NewNop(),
	})

	got, err := uc.Execute(context.Background(), "vo-mcfail", "/tmp/in.mp3", "in.mp3", "folder-x")
	if err == nil {
		t.Fatal("expected error from Execute when MarkCompleted fails, got nil")
	}
	if got != "" {
		t.Errorf("expected empty driveFileID on partial-failure return, got %q", got)
	}
	if !strings.Contains(err.Error(), "MarkCompleted") {
		t.Errorf("expected wrapped MarkCompleted error, got: %v", err)
	}

	status, reason := readIntentStatus(t, repo, "vo-mcfail")
	if status != "failed" {
		t.Errorf("USER-SPEC REQUIREMENT: row MUST be at 'failed' (NOT 'finalized', NOT 'completed') on MarkCompleted-FAIL; got status=%q.\n"+
			"Step 5 is the FINAL lifecycle stamp; 'completed' would be a fake-availability signal, "+
			"'finalized' would be invisible to BOTH sweeper and operator.", status)
	}
	if !strings.Contains(reason, "mark_completed_failed") {
		t.Errorf("expected reason to contain 'mark_completed_failed', got %q", reason)
	}
	if len(repo.markedFailed) != 1 {
		t.Errorf("USER-SPEC REQUIREMENT: MarkFailed MUST fire on MarkCompleted-FAIL (godlike/07 NO_FAKE_AVAILABILITY); "+
			"got %d MarkFailed calls: %+v", len(repo.markedFailed), repo.markedFailed)
	}
	if len(repo.markedCompleted) != 1 {
		t.Errorf("MarkCompleted expected = 1 (the failing call), got %d", len(repo.markedCompleted))
	}
	if drive.calls != 1 {
		t.Errorf("Drive.UploadFile expected = 1, got %d", drive.calls)
	}
	if len(finalizer.calls) != 1 {
		t.Errorf("Finalizer.Finalize expected = 1, got %d", len(finalizer.calls))
	}
	if len(repo.markedUploaded) != 1 {
		t.Errorf("MarkUploaded expected = 1 (Step 3), got %d", len(repo.markedUploaded))
	}
	if len(repo.markedFinalized) != 1 {
		t.Errorf("MarkFinalized expected = 1 (Step 4.5), got %d", len(repo.markedFinalized))
	}
}

// Drive upload FAIL: row must be at 'failed' with reason
// 'upload_failed: ...'. MarkUploaded/Finalize/MarkCompleted must NOT
// be called.
func TestUploadIntentUseCase_DriveUploadFailure_LeavesRowAtFailed(t *testing.T) {
	repo := newMockRepoWithDB(t)
	drive := &mockPublisher{cannedErr: errors.New("drive api 503 service unavailable")}
	finalizer := &mockFinalizer{}
	uc := NewUploadIntentUseCase(UploadIntentDeps{
		Repo:             repo,
		Publisher:        drive,
		ProjectFinalizer: finalizer,
		Logger:           zap.NewNop(),
	})

	got, err := uc.Execute(context.Background(), "vo-drive-fail", "/tmp/in.mp3", "in.mp3", "folder-x")
	if err == nil {
		t.Fatal("expected error from Execute when Drive upload fails, got nil")
	}
	if got != "" {
		t.Errorf("expected empty driveFileID on Drive failure, got %q", got)
	}

	status, reason := readIntentStatus(t, repo, "vo-drive-fail")
	if status != "failed" {
		t.Errorf("expected row at 'failed', got %q", status)
	}
	if !strings.Contains(reason, "upload_failed") {
		t.Errorf("expected reason 'upload_failed: ...', got %q", reason)
	}
	if drive.calls != 1 {
		t.Errorf("Drive.UploadFile called = %d", drive.calls)
	}
	if len(repo.markedUploaded) != 0 || len(repo.markedFinalized) != 0 || len(repo.markedCompleted) != 0 {
		t.Errorf("post-drive-fail steps must NOT run: markedUploaded=%d markedFinalized=%d markedCompleted=%d",
			len(repo.markedUploaded), len(repo.markedFinalized), len(repo.markedCompleted))
	}
	if len(finalizer.calls) != 0 {
		t.Errorf("Finalize must NOT be called after Drive failure")
	}
}

// Idempotent-replay note (forward-pointer): commit A/2 does NOT
// implement pre-Step-2 short-circuit on a row already at
// 'completed'. A replay will fire Step-1 InsertTx, which surfaces
// the mattn/go-sqlite3 UNIQUE-violation error (text: "UNIQUE
// constraint failed"); the use case currently propagates this as
// a wrapped error. Forward-pointer: a future commit (tracked in
// architecture/current.yaml#upload_intents_replay_fix) will add
// a pre-Step-2 `GetStatus()`-style port helper that detects
// 'completed' rows and returns the cached driveFileID without
// re-issuing Drive.UploadFile or touching the Mark* state machine.

// Fail-fast guards: empty voiceoverID / localPath / folderID surface
// typed errors (godlike/05 wiring-error rule, never panicked).
func TestUploadIntentUseCase_FailFastGuards(t *testing.T) {
	repo := newMockRepoWithDB(t)
	drive := &mockPublisher{cannedFileID: "drive"}
	finalizer := &mockFinalizer{}
	uc := NewUploadIntentUseCase(UploadIntentDeps{
		Repo:             repo,
		Publisher:        drive,
		ProjectFinalizer: finalizer,
		Logger:           zap.NewNop(),
	})
	cases := []struct {
		name    string
		v, l, f string
	}{
		{"empty voiceoverID", "", "/tmp/in.mp3", "folder-x"},
		{"empty localPath", "vo-1", "", "folder-x"},
		{"empty folderID", "vo-1", "/tmp/in.mp3", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := uc.Execute(context.Background(), tc.v, tc.l, "fn", tc.f)
			if err == nil {
				t.Errorf("expected error on %s, got nil", tc.name)
			}
		})
	}
	// Verify no DB writes happened (BeginTx count unchanged).
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.startedTx != 0 {
		t.Errorf("expected zero BeginTx on fail-fast guards, got %d", repo.startedTx)
	}
	// Drive.UploadFile also MUST NOT have been called.
	if drive.calls != 0 {
		t.Errorf("Drive.UploadFile called during fail-fast guards; expected 0")
	}
}

// Sanity: ErrUploadIntentNotFound (the application-layer sentinel)
// is non-nil and has the expected text — not a compile-time regression tripwire.
func TestUploadIntentUseCase_SentinelImportSanity(t *testing.T) {
	if ErrUploadIntentNotFound == nil {
		t.Fatal("ErrUploadIntentNotFound must be non-nil (application-layer sentinel)")
	}
	if !strings.Contains(ErrUploadIntentNotFound.Error(), "row not found") {
		t.Errorf("sentinel text unexpected: %q", ErrUploadIntentNotFound.Error())
	}
}
