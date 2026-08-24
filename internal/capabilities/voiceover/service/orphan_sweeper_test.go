// Package voiceover — orphan_sweeper_test.go (audit P0 #4 commit B/2, July 2026).
//
// TDD coverage for OrphanSweeper.sweep() + OrphanSweeper.Run().
// Uses a slim UploadIntentsRepository test double (only
// ListPending + MarkFailed are exercised by the sweeper; unused
// methods panic on call so a future regression that accidentally
// imports another method is loud at test time rather than
// silent-ok-at-runtime).
//
// Metrics: ALL tests construct local
// prometheus.NewCounter / NewCounterVec (NOT promauto) to avoid
// polluting the default Prometheus registry on every test run
// (the promauto package auto-registers named globals).
package voiceover

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.uber.org/zap"
)

// ─── Test doubles ──────────────────────────────────────────────────────────

// sweepMockRepo satisfies UploadIntentsRepository. ONLY
// ListPending + MarkFailed are exercised by OrphanSweeper.
// Unused methods panic on call so a future regression that
// accidentally imports another method is loud at test time
// rather than silent-ok-at-runtime (godlike/07 contract pinning).
type sweepMockRepo struct {
	// Test-controlled fields for the two methods under test.
	listPendingResult []UploadIntent
	listPendingErr    error

	markFailedCalls []markFailedCall
	markFailedErr   error
	markFailedFound bool // false (default) → returns ErrUploadIntentNotFound
}

type markFailedCall struct {
	VoiceoverID string
	Reason      string
}

// Used methods (under test):
func (m *sweepMockRepo) ListPending(ctx context.Context, olderThan time.Time) ([]UploadIntent, error) {
	return m.listPendingResult, m.listPendingErr
}

func (m *sweepMockRepo) MarkFailed(ctx context.Context, voiceoverID, reason string) error {
	m.markFailedCalls = append(m.markFailedCalls, markFailedCall{VoiceoverID: voiceoverID, Reason: reason})
	if m.markFailedErr != nil {
		return m.markFailedErr
	}
	if !m.markFailedFound {
		// Mirror the production concrete's wrap format so
		// isUploadIntentNotFound(...) returns true via errors.Is.
		return fmt.Errorf("%w (target=failed, voiceover_id=%s)", ErrUploadIntentNotFound, voiceoverID)
	}
	return nil
}

// Unused methods (panic on call):

func (m *sweepMockRepo) BeginTx(context.Context) (*sql.Tx, error) {
	panic("sweepMockRepo.BeginTx: orphan sweeper does NOT use BeginTx (caller-owned INSERT tx is the use case's responsibility, not the sweeper's)")
}

func (m *sweepMockRepo) InsertTx(context.Context, *sql.Tx, *UploadIntentInsertOptions) (int64, error) {
	panic("sweepMockRepo.InsertTx: orphan sweeper does NOT call InsertTx")
}

func (m *sweepMockRepo) MarkUploaded(context.Context, string, string) error {
	panic("sweepMockRepo.MarkUploaded: orphan sweeper does NOT call MarkUploaded")
}

func (m *sweepMockRepo) MarkFinalized(context.Context, string) error {
	panic("sweepMockRepo.MarkFinalized: orphan sweeper does NOT call MarkFinalized")
}

func (m *sweepMockRepo) MarkCompleted(context.Context, string) error {
	panic("sweepMockRepo.MarkCompleted: orphan sweeper does NOT call MarkCompleted")
}

// ─── Drive + Metrics mocks ────────────────────────────────────────────────

type sweepMockDrive struct {
	trashCalls []string // fileID order
	trashErr   error
}

func (m *sweepMockDrive) Trash(ctx context.Context, fileID string) error {
	m.trashCalls = append(m.trashCalls, fileID)
	return m.trashErr
}

// Compile-time assertion: mock satisfies OrphanDriveDeleter.
var _ OrphanDriveDeleter = (*sweepMockDrive)(nil)

// newTestMetrics allocates local Prometheus counters (registered
// on the default registry per `prometheus.NewCounter` semantics;
// tests just don't include them in observed populations because
// we read them via testutil.ToFloat64 right after the test
// signals, not via the registered global aggregation).
func newTestMetrics() *Metrics {
	return &Metrics{
		Runs: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "test_orphan_sweeper_runs_total",
			Help: "test counter for orphan sweeper runs",
		}),
		Reconciled: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "test_orphan_sweeper_reconciled_total",
			Help: "test counter for orphan sweeper reconciliations",
		}, []string{"outcome"}),
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────

func uploadIntentStale(id, driveID, status string, age time.Duration) UploadIntent {
	return UploadIntent{
		VoiceoverID: id,
		DriveFileID: driveID,
		Status:      status,
		UpdatedUnix: time.Now().Add(-age).Unix(),
	}
}

func newSweeper(repo UploadIntentsRepository, drive OrphanDriveDeleter, m *Metrics, pendingTTL, uploadedTTL time.Duration) *OrphanSweeper {
	return NewOrphanSweeper(OrphanSweeperDeps{
		Repo:         repo,
		DriveDeleter: drive,
		Logger:       zap.NewNop(),
		Metrics:      m,
		Tick:         10 * time.Minute,
		PendingTTL:   pendingTTL,
		UploadedTTL:  uploadedTTL,
	})
}

// ─── Tests ────────────────────────────────────────────────────────────────

// Test 1: Both stale pending + uploaded, both MarkFailed + Drive.Trash
// success. Verify 2 reconciliations, 1 drive trash call, 2 metrics.
func TestOrphanSweeper_sweep_BothStale_BothCompensated(t *testing.T) {
	repo := &sweepMockRepo{
		listPendingResult: []UploadIntent{
			uploadIntentStale("vo-pending-1", "", "pending", 60*time.Minute),              // older than pendingTTL=30m
			uploadIntentStale("vo-uploaded-1", "drive-stuck", "uploaded", 90*time.Minute), // older than uploadedTTL=60m
		},
		markFailedFound: true,
	}
	drive := &sweepMockDrive{}
	m := newTestMetrics()
	s := newSweeper(repo, drive, m, 30*time.Minute, 60*time.Minute)

	stats, err := s.sweep(context.Background())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if stats.PendingStale != 1 || stats.UploadedStale != 1 {
		t.Errorf("list counts: pending=%d uploaded=%d, want pending=1 uploaded=1",
			stats.PendingStale, stats.UploadedStale)
	}
	if stats.PendingDone != 1 || stats.UploadedDone != 1 {
		t.Errorf("done counts: pending=%d uploaded=%d, want pending=1 uploaded=1",
			stats.PendingDone, stats.UploadedDone)
	}
	if stats.TrashErrors != 0 || stats.MarkFailedErrs != 0 {
		t.Errorf("errors: trash=%d markfailed=%d, want both 0",
			stats.TrashErrors, stats.MarkFailedErrs)
	}

	// MarkFailed calls: 1 for pending (pending_timeout reason), 1 for uploaded (uploaded_no_finalize reason).
	if len(repo.markFailedCalls) != 2 {
		t.Fatalf("MarkFailed calls = %d, want 2", len(repo.markFailedCalls))
	}
	if repo.markFailedCalls[0].VoiceoverID != "vo-pending-1" ||
		repo.markFailedCalls[0].Reason != "orphan_sweep: pending_timeout" {
		t.Errorf("pending MarkFailed: %+v, want vo-pending-1/pending_timeout", repo.markFailedCalls[0])
	}
	if repo.markFailedCalls[1].VoiceoverID != "vo-uploaded-1" ||
		repo.markFailedCalls[1].Reason != "orphan_sweep: uploaded_no_finalize" {
		t.Errorf("uploaded MarkFailed: %+v, want vo-uploaded-1/uploaded_no_finalize", repo.markFailedCalls[1])
	}

	// Drive.Trash: 1 call with drive-stuck.
	if len(drive.trashCalls) != 1 || drive.trashCalls[0] != "drive-stuck" {
		t.Errorf("Trash calls = %v, want [drive-stuck]", drive.trashCalls)
	}

	// Metrics: 1 each pending_timeout + uploaded_cleanup.
	if v := testutil.ToFloat64(m.Reconciled.WithLabelValues(OutcomePendingTimeout)); v != 1 {
		t.Errorf("Reconciled{pending_timeout} = %v, want 1", v)
	}
	if v := testutil.ToFloat64(m.Reconciled.WithLabelValues(OutcomeUploadedCleanup)); v != 1 {
		t.Errorf("Reconciled{uploaded_cleanup} = %v, want 1", v)
	}
}

// Test 2: Pending row NEWER than pendingTTL → not compensated
// (TTL filter excludes it). The LIST still counts the row as
// stale (PendingStale=1) because ListPending returns it; the TTL
// gate is the per-row filter inside sweep().
func TestOrphanSweeper_sweep_PendingNewerThanTTL_Omitted(t *testing.T) {
	repo := &sweepMockRepo{
		listPendingResult: []UploadIntent{
			uploadIntentStale("vo-pending-young", "", "pending", 5*time.Minute), // newer than pendingTTL=30m
		},
		markFailedFound: true,
	}
	drive := &sweepMockDrive{}
	m := newTestMetrics()
	s := newSweeper(repo, drive, m, 30*time.Minute, 60*time.Minute)

	stats, err := s.sweep(context.Background())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if stats.PendingStale != 1 {
		t.Errorf("PendingStale = %d, want 1 (ListPending counts the row even if TTL filters it)", stats.PendingStale)
	}
	if stats.PendingDone != 0 {
		t.Errorf("PendingDone = %d, want 0 (TTL filter)", stats.PendingDone)
	}
	if len(repo.markFailedCalls) != 0 {
		t.Errorf("MarkFailed calls %d, want 0", len(repo.markFailedCalls))
	}
	if v := testutil.ToFloat64(m.Reconciled.WithLabelValues(OutcomePendingTimeout)); v != 0 {
		t.Errorf("Reconciled{pending_timeout} = %v, want 0", v)
	}
}

// Test 3: Uploaded row NEWER than uploadedTTL → not compensated.
func TestOrphanSweeper_sweep_UploadedNewerThanTTL_Omitted(t *testing.T) {
	repo := &sweepMockRepo{
		listPendingResult: []UploadIntent{
			uploadIntentStale("vo-uploaded-young", "drive-young", "uploaded", 20*time.Minute), // newer than uploadedTTL=60m
		},
		markFailedFound: true,
	}
	drive := &sweepMockDrive{}
	m := newTestMetrics()
	s := newSweeper(repo, drive, m, 30*time.Minute, 60*time.Minute)

	stats, err := s.sweep(context.Background())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if stats.UploadedStale != 1 {
		t.Errorf("UploadedStale = %d, want 1", stats.UploadedStale)
	}
	if stats.UploadedDone != 0 {
		t.Errorf("UploadedDone = %d, want 0 (TTL filter)", stats.UploadedDone)
	}
	if len(repo.markFailedCalls) != 0 {
		t.Errorf("MarkFailed calls %d, want 0", len(repo.markFailedCalls))
	}
	if len(drive.trashCalls) != 0 {
		t.Errorf("Trash calls %v, want []", drive.trashCalls)
	}
}

// Test 4: Drive.Trash error → MarkFailed STILL FIRES (so sweeper
// doesn't loop on the same row indefinitely). The intent-level
// reconcile metric IS incremented (godlike/07 NO_FAKE_AVAILABILITY:
// operator visibility on intent state is the canonical signal;
// the broken Trash is reported via TrashErrors stat + per-row
// warn-level log).
func TestOrphanSweeper_sweep_TrashError_StillMarkFailed(t *testing.T) {
	repo := &sweepMockRepo{
		listPendingResult: []UploadIntent{
			uploadIntentStale("vo-trash-fail", "drive-stuck", "uploaded", 90*time.Minute),
		},
		markFailedFound: true,
	}
	drive := &sweepMockDrive{trashErr: errors.New("drive api 503 service unavailable")}
	m := newTestMetrics()
	s := newSweeper(repo, drive, m, 30*time.Minute, 60*time.Minute)

	stats, err := s.sweep(context.Background())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if stats.TrashErrors != 1 {
		t.Errorf("TrashErrors = %d, want 1", stats.TrashErrors)
	}
	if stats.UploadedDone != 1 {
		t.Errorf("UploadedDone = %d, want 1 (intent row compensated even when Trash fails — godlike/07 no loop)", stats.UploadedDone)
	}
	if len(repo.markFailedCalls) != 1 {
		t.Errorf("MarkFailed calls %d, want 1", len(repo.markFailedCalls))
	}
	if v := testutil.ToFloat64(m.Reconciled.WithLabelValues(OutcomeUploadedCleanup)); v != 1 {
		t.Errorf("Reconciled{uploaded_cleanup} = %v, want 1 (intent-level recovery succeeded)", v)
	}
}

// Test 5: MarkFailed returns NotFound (idempotent race: another
// sweeper already moved the row to 'failed') → outcomeIdempotentSkip
// — NO metric bump, NO MarkFailedErrs increment. The Trash side
// still happened (because Trash fires BEFORE MarkFailed in
// compensateUploaded).
func TestOrphanSweeper_sweep_MarkFailedNotFound_IdempotentSkip(t *testing.T) {
	repo := &sweepMockRepo{
		listPendingResult: []UploadIntent{
			uploadIntentStale("vo-already-failed", "drive-stuck", "uploaded", 90*time.Minute),
		},
		markFailedFound: false, // simulate another sweeper already moved to 'failed'
	}
	drive := &sweepMockDrive{}
	m := newTestMetrics()
	s := newSweeper(repo, drive, m, 30*time.Minute, 60*time.Minute)

	stats, err := s.sweep(context.Background())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if stats.UploadedDone != 0 {
		t.Errorf("UploadedDone = %d, want 0 (idempotent skip — another sweeper won)", stats.UploadedDone)
	}
	if stats.MarkFailedErrs != 0 {
		t.Errorf("MarkFailedErrs = %d, want 0 (NotFound isn't an error per godlike/07 idempotency)", stats.MarkFailedErrs)
	}
	if v := testutil.ToFloat64(m.Reconciled.WithLabelValues(OutcomeUploadedCleanup)); v != 0 {
		t.Errorf("Reconciled{uploaded_cleanup} = %v, want 0 (idempotent skip)", v)
	}
	// Trash WAS called (it's before MarkFailed in compensateUploaded).
	if len(drive.trashCalls) != 1 || drive.trashCalls[0] != "drive-stuck" {
		t.Errorf("Trash calls = %v, want [drive-stuck] (Trash fires before MarkFailed)", drive.trashCalls)
	}
}

// Test 6: MarkFailed returns non-NotFound, non-nil error →
// outcomeError. NO metric (godlike/07: a metric bump implies
// canonical recovery, not "tried but failed"). MarkFailedErrs
// stat IS incremented so operators can see the failure cluster.
func TestOrphanSweeper_sweep_MarkFailedError_NotCompensated(t *testing.T) {
	repo := &sweepMockRepo{
		listPendingResult: []UploadIntent{
			uploadIntentStale("vo-mf-fail", "", "pending", 60*time.Minute),
		},
		markFailedErr: errors.New("sqlite database is locked (transient)"),
	}
	drive := &sweepMockDrive{}
	m := newTestMetrics()
	s := newSweeper(repo, drive, m, 30*time.Minute, 60*time.Minute)

	stats, err := s.sweep(context.Background())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if stats.PendingDone != 0 {
		t.Errorf("PendingDone = %d, want 0 (MarkFailed errored)", stats.PendingDone)
	}
	if stats.MarkFailedErrs != 1 {
		t.Errorf("MarkFailedErrs = %d, want 1", stats.MarkFailedErrs)
	}
	if v := testutil.ToFloat64(m.Reconciled.WithLabelValues(OutcomePendingTimeout)); v != 0 {
		t.Errorf("Reconciled{pending_timeout} = %v, want 0 (MarkFailed errored — no false success)", v)
	}
}

// Test 7: ListPending returns error → sweep returns wrapped error,
// NO MarkFailed calls, NO Drive.Trash calls, NO metric bumps.
// Sweep loop continues with next tick (Run handles the error
// recovery).
func TestOrphanSweeper_sweep_ListPendingError_ReturnsErr(t *testing.T) {
	repo := &sweepMockRepo{
		listPendingErr: errors.New("db connection lost (transient)"),
	}
	drive := &sweepMockDrive{}
	m := newTestMetrics()
	s := newSweeper(repo, drive, m, 30*time.Minute, 60*time.Minute)

	_, err := s.sweep(context.Background())
	if err == nil {
		t.Fatal("sweep: expected error from ListPending failure, got nil")
	}
	if !errors.Is(err, repo.listPendingErr) {
		t.Errorf("sweep err: %v, want wrap of %v", err, repo.listPendingErr)
	}
	if len(repo.markFailedCalls) != 0 {
		t.Errorf("MarkFailed calls %d, want 0 (ListPending errored)", len(repo.markFailedCalls))
	}
	if len(drive.trashCalls) != 0 {
		t.Errorf("Trash calls %v, want []", drive.trashCalls)
	}
	if v := testutil.ToFloat64(m.Reconciled.WithLabelValues(OutcomePendingTimeout)); v != 0 {
		t.Errorf("Reconciled{pending_timeout} = %v, want 0", v)
	}
	if v := testutil.ToFloat64(m.Reconciled.WithLabelValues(OutcomeUploadedCleanup)); v != 0 {
		t.Errorf("Reconciled{uploaded_cleanup} = %v, want 0", v)
	}
}

// Test 8: Run() goroutine — Runs counter increments ONCE on entry
// (per process boot, NOT per sweep tick). The test starts the
// goroutine, briefly lets it initialize, then cancels ctx and
// confirms the goroutine exits within a bounded timeout. The
// canonical tick test for the loop bookkeeping invariant.
func TestOrphanSweeper_Run_IncrementsRunsCounterOnce(t *testing.T) {
	repo := &sweepMockRepo{
		listPendingResult: []UploadIntent{}, // empty: nothing to reconcile
		markFailedFound:   true,
	}
	drive := &sweepMockDrive{}
	m := newTestMetrics()
	s := newSweeper(repo, drive, m, 30*time.Minute, 60*time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()

	// Give the goroutine a moment to register Runs.Inc().
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after ctx cancel (2s timeout)")
	}

	if v := testutil.ToFloat64(m.Runs); v != 1 {
		t.Errorf("Runs counter = %v, want 1 (ONCE per Run invocation, NOT per sweep tick)", v)
	}
}
