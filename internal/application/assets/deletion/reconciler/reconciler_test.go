// Package reconciler — reconciler_test.go (Blocco 3.2 commit 2/2, June 2026)
//
// Unit tests for the deletion-reconciler Service + Classify pure
// function. Mirrors qdrant/reconciler/service_test.go style:
// in-memory mocks for ports, hand-built StuckRow literals, no
// real DB.
package reconciler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Mocks ──────────────────────────────────────────────────────────

// stubScanner returns the rows configured at construction time
// regardless of (now, threshold). For tests that exercise a
// specific stuck-row set, build a literal []StuckRow.
type stubScanner struct {
	mu    sync.Mutex
	rows  []StuckRow
	err   error
	calls int
}

func (s *stubScanner) ListStuckRows(_ time.Time, _ time.Duration) ([]StuckRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	out := make([]StuckRow, len(s.rows))
	copy(out, s.rows)
	return out, nil
}

// recorderMetrics captures every Record* call so tests can assert
// on the rolled-up (action, from_state, reason) counters.
type recorderMetrics struct {
	mu              sync.Mutex
	repairs         map[string]int // action+":"+fromState → count
	skips           map[string]int // reason → count
	errored         int
	runCompletions  int
	lastDurationSec float64
}

func newRecorderMetrics() *recorderMetrics {
	return &recorderMetrics{
		repairs: map[string]int{},
		skips:   map[string]int{},
	}
}

func (m *recorderMetrics) RecordRepair(action, fromState string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.repairs[action+":"+fromState]++
}

func (m *recorderMetrics) RecordSkipped(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.skips[reason]++
}

func (m *recorderMetrics) RecordErrored() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errored++
}

func (m *recorderMetrics) RecordRunComplete(durationSeconds float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runCompletions++
	m.lastDurationSec = durationSeconds
}

// stubEnqueuer records each Enqueue* call and returns the
// pre-programmed error verbatim. Mirrors drive_delete_test.go's
// "mock records the call" pattern.
type stubEnqueuer struct {
	mu                 sync.Mutex
	driveCalls         []driveCallRecord
	indexCalls         []string
	driveErr           error
	indexErr           error
	driveCallsByIDFunc func(string) error
	indexCallsByIDFunc func(string) error
}

// driveCallRecord captures the (assetID, permanently) tuple so
// tests can assert the reconciler passes the safe-fallback
// permanently=false flag.
type driveCallRecord struct {
	assetID     string
	permanently bool
}

func (s *stubEnqueuer) EnqueueDriveDelete(_ context.Context, assetID string, permanently bool) error {
	s.mu.Lock()
	s.driveCalls = append(s.driveCalls, driveCallRecord{assetID: assetID, permanently: permanently})
	s.mu.Unlock()
	if s.driveCallsByIDFunc != nil {
		return s.driveCallsByIDFunc(assetID)
	}
	return s.driveErr
}

func (s *stubEnqueuer) EnqueueIndexDelete(_ context.Context, assetID string) error {
	s.mu.Lock()
	s.indexCalls = append(s.indexCalls, assetID)
	s.mu.Unlock()
	if s.indexCallsByIDFunc != nil {
		return s.indexCallsByIDFunc(assetID)
	}
	return s.indexErr
}

// ── Classify unit tests ────────────────────────────────────────────

func TestClassify_RequeueDriveForDELETE_REQUESTED(t *testing.T) {
	r := Classify(StuckRow{
		AssetID: "asset-a",
		State:   string(asset.StateDeleteRequested),
	})
	if r.Action != ActionRequeueDrive {
		t.Errorf("Action: want %q got %q", ActionRequeueDrive, r.Action)
	}
	if r.Skip != "" {
		t.Errorf("Skip must be empty when action is set; got %q", r.Skip)
	}
}

func TestClassify_RequeueIndexForMidChainStates(t *testing.T) {
	for _, state := range []asset.LifecycleState{
		asset.StateDriveDeletePending,
		asset.StateLifecycleIndexDeletePending,
	} {
		r := Classify(StuckRow{
			AssetID: "asset-a",
			State:   string(state),
		})
		if r.Action != ActionRequeueIndex {
			t.Errorf("state=%s: Action want %q got %q", state, ActionRequeueIndex, r.Action)
		}
		if r.Skip != "" {
			t.Errorf("state=%s: Skip must be empty when action is set; got %q", state, r.Skip)
		}
	}
}

func TestClassify_SkipTerminalStates(t *testing.T) {
	// DELETED + ACTIVE + STAGING + PROCESSING + ERROR are
	// unreachable by the scanner's WHERE (it filters to deletion-
	// chain states) but a defensive Classify covers drift:
	// - DELETED is already-terminal (no action needed)
	// - ACTIVE/STAGING/PROCESSING/ERROR are NOT deletion chain
	//   (would be a config drift if seen)
	for _, state := range []asset.LifecycleState{
		asset.StateDeleted,
		asset.StateActive,
		asset.StateStaging,
		asset.StateProcessing,
		asset.StateError,
	} {
		r := Classify(StuckRow{
			AssetID: "asset-a",
			State:   string(state),
		})
		if r.Action != "" {
			t.Errorf("state=%s: Action must be empty (skipped); got %q", state, r.Action)
		}
		if r.Skip == "" {
			t.Errorf("state=%s: Skip reason must be non-empty when action is empty", state)
		}
	}
}

// ── Service unit tests ─────────────────────────────────────────────

func newTestService(t *testing.T, scanner Scanner, enq OutboxEnqueuer, m Metrics, clock func() time.Time) *Service {
	t.Helper()
	return NewServiceFromDeps(ServiceDeps{
		Scanner:          scanner,
		OutboxEnqueuer:   enq,
		Metrics:          m,
		Clock:            clock,
		Log:              zap.NewNop(),
		DefaultInterval:  15 * time.Minute,
		DefaultThreshold: 30 * time.Minute,
	})
}

// TestService_PanicsOnNilScanner confirms the required-port
// nil-panic. PR-10 doctrine: silent no-op dispatch is the canonical
// regression in pre-Wave-21 wiring; we trip the panic instead.
func TestService_PanicsOnNilScanner(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("NewServiceFromDeps with nil Scanner must panic")
		}
	}()
	_ = NewServiceFromDeps(ServiceDeps{
		OutboxEnqueuer: &stubEnqueuer{},
	})
}

// TestService_PanicsOnNilEnqueuer confirms the required-port nil-
// panic for OutboxEnqueuer.
func TestService_PanicsOnNilEnqueuer(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("NewServiceFromDeps with nil OutboxEnqueuer must panic")
		}
	}()
	_ = NewServiceFromDeps(ServiceDeps{
		Scanner: &stubScanner{},
	})
}

// TestReconcileOnce_HappyPathMixedActions covers the canonical
// 3-stuck-rows case: 1 DELETE_REQUESTED → drive requeue,
// 2 {DRIVE_DELETE_PENDING, INDEX_DELETE_PENDING} → index requeue.
// All 3 dispatches succeed; metrics emit per (action, from_state).
func TestReconcileOnce_HappyPathMixedActions(t *testing.T) {
	rows := []StuckRow{
		{AssetID: "a-drive", State: string(asset.StateDeleteRequested)},
		{AssetID: "a-index-pending", State: string(asset.StateDriveDeletePending)},
		{AssetID: "a-index-infight", State: string(asset.StateLifecycleIndexDeletePending)},
	}
	scanner := &stubScanner{rows: rows}
	enq := &stubEnqueuer{}
	m := newRecorderMetrics()
	s := newTestService(t, scanner, enq, m, nil)

	report, err := s.ReconcileOnce(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}

	if report.RowsScanned != 3 {
		t.Errorf("RowsScanned: want 3 got %d", report.RowsScanned)
	}
	if report.RowsRequeueDrive != 1 {
		t.Errorf("RowsRequeueDrive: want 1 got %d", report.RowsRequeueDrive)
	}
	if report.RowsRequeueIndex != 2 {
		t.Errorf("RowsRequeueIndex: want 2 got %d", report.RowsRequeueIndex)
	}
	if report.RowsSkipped != 0 || report.RowsErrored != 0 {
		t.Errorf("zero skip/err expected; got skip=%d errored=%d", report.RowsSkipped, report.RowsErrored)
	}
	if scanner.calls != 1 {
		t.Errorf("Scanner must be called exactly once per tick; got %d", scanner.calls)
	}

	// Verify per-(action, from_state) metric bumps.
	if m.repairs["requeue_drive:DELETE_REQUESTED"] != 1 {
		t.Errorf("metric requeue_drive:DELETE_REQUESTED: want 1 got %d", m.repairs["requeue_drive:DELETE_REQUESTED"])
	}
	if m.repairs["requeue_index:DRIVE_DELETE_PENDING"] != 1 {
		t.Errorf("metric requeue_index:DRIVE_DELETE_PENDING: want 1 got %d", m.repairs["requeue_index:DRIVE_DELETE_PENDING"])
	}
	if m.repairs["requeue_index:INDEX_DELETE_PENDING"] != 1 {
		t.Errorf("metric requeue_index:INDEX_DELETE_PENDING: want 1 got %d", m.repairs["requeue_index:INDEX_DELETE_PENDING"])
	}
	if m.runCompletions != 1 {
		t.Errorf("RunComplete must fire exactly once per tick; got %d", m.runCompletions)
	}

	// Verify enqueuer routing:
	if len(enq.driveCalls) != 1 || enq.driveCalls[0].assetID != "a-drive" {
		t.Errorf("driveCalls: want [{a-drive false}]; got %+v", enq.driveCalls)
	}
	// Safe-fallback contract: reconciler MUST pass permanently=false
	// (Trash route) regardless of the original intent (reconciliation
	// is a recovery operation, not a user-initiated one).
	if enq.driveCalls[0].permanently != false {
		t.Errorf("driveCalls[0].permanently: want false (safe-fallback); got %v", enq.driveCalls[0].permanently)
	}
	if len(enq.indexCalls) != 2 {
		t.Errorf("indexCalls: want 2; got %v", enq.indexCalls)
	}
	// Both index calls are for the 2 in-flight rows.
	got := map[string]bool{}
	for _, id := range enq.indexCalls {
		got[id] = true
	}
	if !got["a-index-pending"] || !got["a-index-infight"] {
		t.Errorf("indexCalls content wrong: %v", enq.indexCalls)
	}
}

// TestReconcileOnce_ScannerErrorFailsClosed confirms Phase 1 SQL
// error aborts the run (no partial dispatch; Reports a non-nil
// error, sees 1 errored, zero dispatches).
func TestReconcileOnce_ScannerErrorFailsClosed(t *testing.T) {
	scanner := &stubScanner{err: errors.New("simulated SQL down")}
	enq := &stubEnqueuer{}
	m := newRecorderMetrics()
	s := newTestService(t, scanner, enq, m, nil)

	report, err := s.ReconcileOnce(context.Background(), RunOptions{})
	if err == nil {
		t.Fatalf("ReconcileOnce must return an error on Phase 1 SQL failure")
	}
	if report.RowsScanned != 0 {
		t.Errorf("RowsScanned must be 0 on Phase 1 error; got %d", report.RowsScanned)
	}
	if report.RowsErrored != 1 {
		t.Errorf("RowsErrored: want 1 (Phase 1 fail); got %d", report.RowsErrored)
	}
	if m.errored != 1 {
		t.Errorf("metrics.errored: want 1; got %d", m.errored)
	}
	if m.runCompletions != 1 {
		t.Errorf("RunComplete must still fire on Phase 1 error (dashboard signal); got %d", m.runCompletions)
	}
	if len(enq.driveCalls) != 0 || len(enq.indexCalls) != 0 {
		t.Errorf("no dispatches on Phase 1 error; got drive=%v index=%v", enq.driveCalls, enq.indexCalls)
	}
}

// TestReconcileOnce_OneRowDispatchErrorContinuesRun confirms a
// transient dispatch failure on ONE row does NOT abort the run;
// other rows still dispatch + counters reflect the per-row outcome.
func TestReconcileOnce_OneRowDispatchErrorContinuesRun(t *testing.T) {
	row1 := "a-drive-fail"
	row2 := "a-drive-ok"
	rows := []StuckRow{
		{AssetID: row1, State: string(asset.StateDeleteRequested)},
		{AssetID: row2, State: string(asset.StateDeleteRequested)},
	}
	scanner := &stubScanner{rows: rows}
	enq := &stubEnqueuer{
		driveCallsByIDFunc: func(id string) error {
			if id == row1 {
				return errors.New("dispatch error isolating row")
			}
			return nil
		},
	}
	m := newRecorderMetrics()
	s := newTestService(t, scanner, enq, m, nil)

	report, err := s.ReconcileOnce(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("ReconcileOnce: %v (transient per-row error must NOT abort the run)", err)
	}

	if report.RowsScanned != 2 {
		t.Errorf("RowsScanned: want 2; got %d", report.RowsScanned)
	}
	if report.RowsRequeueDrive != 1 {
		t.Errorf("RowsRequeueDrive: want 1 (only the OK row); got %d", report.RowsRequeueDrive)
	}
	if report.RowsErrored != 1 {
		t.Errorf("RowsErrored: want 1 (the failing row); got %d", report.RowsErrored)
	}
	if m.errored != 1 {
		t.Errorf("metrics.errored: want 1; got %d", m.errored)
	}
	if m.repairs["requeue_drive:DELETE_REQUESTED"] != 1 {
		t.Errorf("metrics.repairs[requeue_drive:DELETE_REQUESTED]: want 1; got %d", m.repairs["requeue_drive:DELETE_REQUESTED"])
	}
}

// TestReconcileOnce_EmptyRowSet is a no-op path: zero scanned rows,
// zero dispatches, RunComplete fires once.
func TestReconcileOnce_EmptyRowSet(t *testing.T) {
	scanner := &stubScanner{rows: nil}
	enq := &stubEnqueuer{}
	m := newRecorderMetrics()
	s := newTestService(t, scanner, enq, m, nil)

	report, err := s.ReconcileOnce(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	if report.RowsScanned != 0 ||
		report.RowsRequeueDrive != 0 ||
		report.RowsRequeueIndex != 0 ||
		report.RowsSkipped != 0 ||
		report.RowsErrored != 0 {
		t.Errorf("empty input must produce zero counters; got %+v", report)
	}
	if m.runCompletions != 1 {
		t.Errorf("RunComplete fires even on empty tick (dashboard signal); want 1 got %d", m.runCompletions)
	}
}

// TestReconcileOnce_SkippedRowIsRoutedMetricOnly covers the
// defensive-code path: Classify returned a Skip (drift case where
// the scanner surfaced a non-deletion-chain row). The row goes to
// RowsSkipped + RecordSkipped("reason") — no dispatch attempted.
func TestReconcileOnce_SkippedRowIsRoutedMetricOnly(t *testing.T) {
	rows := []StuckRow{
		{AssetID: "a-drift", State: string(asset.StateActive)},
	}
	scanner := &stubScanner{rows: rows}
	enq := &stubEnqueuer{}
	m := newRecorderMetrics()
	s := newTestService(t, scanner, enq, m, nil)

	report, err := s.ReconcileOnce(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	if report.RowsSkipped != 1 {
		t.Errorf("RowsSkipped: want 1; got %d", report.RowsSkipped)
	}
	if m.skips["unknown_state:ACTIVE"] != 1 {
		t.Errorf("RecordSkipped with reason unknown_state:ACTIVE: want 1 got %d", m.skips["unknown_state:ACTIVE"])
	}
	if len(enq.driveCalls) != 0 || len(enq.indexCalls) != 0 {
		t.Errorf("skipped row must NOT trigger dispatch; got drive=%v index=%v", enq.driveCalls, enq.indexCalls)
	}
}

// TestReconcileOnce_ClockIsHonored covers the Now() override path:
// a fixed clock is reflected in StartedAt + CompletedAt.
func TestReconcileOnce_ClockIsHonored(t *testing.T) {
	fixedNow := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	scanner := &stubScanner{}
	enq := &stubEnqueuer{}
	m := newRecorderMetrics()
	s := newTestService(t, scanner, enq, m, func() time.Time { return fixedNow })

	report, _ := s.ReconcileOnce(context.Background(), RunOptions{})
	if !report.StartedAt.Equal(fixedNow) {
		t.Errorf("StartedAt: want %v got %v", fixedNow, report.StartedAt)
	}
	if !report.CompletedAt.Equal(fixedNow) {
		t.Errorf("CompletedAt: want %v got %v", fixedNow, report.CompletedAt)
	}
}

// TestRun_CtxCancelExitsLoop drives the Run ticker-cancel path:
// cancel the context within the initial-tick window, expect the
// loop to return promptly. Avoids hanging by using a 1-second
// post-cancel completion check.
func TestRun_CtxCancelExitsLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	scanner := &stubScanner{rows: nil}
	enq := &stubEnqueuer{}
	m := newRecorderMetrics()
	s := NewServiceFromDeps(ServiceDeps{
		Scanner:          scanner,
		OutboxEnqueuer:   enq,
		Metrics:          m,
		Log:              zap.NewNop(),
		DefaultInterval:  50 * time.Millisecond,
		DefaultThreshold: 30 * time.Minute,
	})

	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()
	// Cancel immediately after Run starts (tickOnce already ran).
	cancel()
	select {
	case <-done:
		// Run returned cleanly; cancel-exit works.
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit on ctx.Done within 2s")
	}
}
