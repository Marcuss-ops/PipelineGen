// Package jobs -- cancellation_test.go (FASE 4(b) typed-LeaseState
// integration, July 2026).
//
// Pins the cancellation-plumbing contract under the post-FASE-4
// architecture: the 2-second IsCancelled-poll goroutine (the
// pre-FASE-4 startCancelWatcher helper at worker_execution.go) is
// REMOVED; cancellation now propagates through the typed
// job.RenewLeaseResult.State return value (Continue |
// CancelRequested | LeaseLost) observed by the
// renewLeaseLoopWith helper on every lease-renewal tick.
//
// Test surface (per the FASE 4(b) cut):
//
//  1. TestRenewLeaseLoopWith_CancelRequested_TriggersJobCancel --
//     helper-level test of the typed cancel signal in isolation. A
//     mock JobBroker returns LeaseStateCancelRequested on the
//     first renew call; the renew-loop MUST invoke the supplied
//     jobCancel callback (so the handler's ctx is cancelled).
//
//  2. TestRenewLeaseLoopWith_LeaseLost_AbortsLoop -- mock returns
//     LeaseStateLeaseLost; the renew-loop MUST exit without
//     invoking jobCancel (the worker is orphaned, not cancelled).
//
//  3. TestRenewLeaseLoopWith_Continue_NoOp -- mock returns
//     LeaseStateContinue; the renew-loop continues (no cancel,
//     no log+return).
//
//  4. TestWorker_CancelsRunningJobOnCancelSignal -- end-to-end
//     through Worker.runJob with a mockBroker whose RenewLease
//     returns LeaseStateCancelRequested on the 2nd call. The
//     handler observes ctx.Done() within a 4-second budget; the
//     load-bearing signal is the handler observed ctx.Err() ==
//     context.Canceled AND the renew-loop fired at least once.
//
//  5. TestWorker_UsesCurrentRevisionAtFinalization -- unchanged
//     from the pre-FASE-4 contract (finalizer uses the latest
//     DB revision, not the claim-time snapshot).
//
// Why a 4-second budget (vs the pre-FASE-4 6-second): the
// pre-FASE-4 watcher polled at cancelPollInterval=2s; the
// post-FASE-4 renew-loop ticks at leaseTTL/3 (≥5s in production
// with 30s leaseTTL). To keep the test fast, the mock's
// RenewLease returns CancelRequested on the FIRST call (so
// the 4-second budget covers the initial-tick + a 2-second
// slack for goroutine scheduling).
//
// godlike/06 SSOT: the cancellation contract is now entirely
// typed (LeaseState enum + RenewLeaseResult envelope); the
// pre-FASE-4 3-tier test surface (helper + helper-nil + helper-ctx-done)
// collapses to a 3-state enum test surface in FASE 4(b) —
// see tests 1-3 below.
package jobs

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// renewLoopMockJobBroker is a minimal JobBroker used by the helper-level
// renewLeaseLoopWith tests. Only the RenewLease method has real
// behaviour (returns the configured result+err); every other method
// is a no-op or panics on unexpected dispatch.
type renewLoopMockJobBroker struct {
	mu        sync.Mutex
	renewFunc func(ctx context.Context, id, workerID string, leaseTTL time.Duration) (job.RenewLeaseResult, error)
	getJob    *job.Job
	renewHits int
}

func (m *renewLoopMockJobBroker) RenewLease(ctx context.Context, id, workerID string, leaseTTL time.Duration) (job.RenewLeaseResult, error) {
	m.mu.Lock()
	m.renewHits++
	renewFunc := m.renewFunc
	m.mu.Unlock()
	if renewFunc == nil {
		return job.RenewLeaseResult{State: job.LeaseStateContinue}, nil
	}
	return renewFunc(ctx, id, workerID, leaseTTL)
}

func (m *renewLoopMockJobBroker) hits() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.renewHits
}

// All other JobBroker methods are no-ops (the renew-loop tests do
// not exercise the dispatcher / finalizer / claim path).
func (m *renewLoopMockJobBroker) Create(_ context.Context, _ *job.Job) error { return nil }
func (m *renewLoopMockJobBroker) Get(_ context.Context, _ string) (*job.Job, error) {
	return m.getJob, nil
}
func (m *renewLoopMockJobBroker) List(_ context.Context, _ job.Filter) ([]job.Job, error) {
	return nil, nil
}
func (m *renewLoopMockJobBroker) FindActiveByKey(_ context.Context, _ string) (*job.Job, error) {
	return nil, nil
}
func (m *renewLoopMockJobBroker) FindByTypeAndCorrelation(_ context.Context, _ string, _ string) (*job.Job, error) {
	return nil, nil
}
func (m *renewLoopMockJobBroker) FindByClientAndIdempotencyKey(_ context.Context, _, _ string) (*job.Job, error) {
	return nil, nil
}
func (m *renewLoopMockJobBroker) ListEvents(_ context.Context, _ string) ([]job.Event, error) {
	return nil, nil
}
func (m *renewLoopMockJobBroker) Retry(_ context.Context, _ string) (*job.Job, error) {
	return nil, nil
}
func (m *renewLoopMockJobBroker) ClaimNext(_ context.Context, _ string, _ time.Duration, _ []string) (*job.Job, error) {
	return nil, nil
}
func (m *renewLoopMockJobBroker) Complete(_ context.Context, _ string, _ string, _ string, _ int, _ json.RawMessage) error {
	return nil
}
func (m *renewLoopMockJobBroker) Fail(_ context.Context, _ string, _ string, _ string, _ int, _ string) error {
	return nil
}
func (m *renewLoopMockJobBroker) ScheduleRetry(_ context.Context, _ string, _ string, _ string, _ int, _ string, _ time.Duration) error {
	return nil
}
func (m *renewLoopMockJobBroker) Cancel(_ context.Context, _ string) error { return nil }
func (m *renewLoopMockJobBroker) SetProgress(_ context.Context, _ string, _ int, _ string) error {
	return nil
}
func (m *renewLoopMockJobBroker) AddEvent(_ context.Context, _ string, _ string, _ string, _ map[string]any) error {
	return nil
}
func (m *renewLoopMockJobBroker) DeadLetter(_ context.Context, _ string, _ string) error {
	return nil
}
func (m *renewLoopMockJobBroker) FinalizeAttempt(_ context.Context, _ job.FinalizeAttemptCommand) (job.FinalizeAttemptResult, error) {
	return job.FinalizeAttemptResult{}, nil
}

var _ job.JobBroker = (*renewLoopMockJobBroker)(nil)

// ── Test 1: LeaseStateCancelRequested triggers jobCancel ───────────

// TestRenewLeaseLoopWith_CancelRequested_TriggersJobCancel pins the
// FASE 4(b) typed cancel signal: when the broker's RenewLease returns
// LeaseStateCancelRequested, the renew-loop MUST invoke the supplied
// jobCancel callback (so the handler's ctx is cancelled) and exit.
// Pre-FASE-4 this contract was implemented by a 2-second IsCancelled-
// poll goroutine; FASE 4(b) folds the cancel signal into the typed
// lease-renewal envelope.
func TestRenewLeaseLoopWith_CancelRequested_TriggersJobCancel(t *testing.T) {
	t.Parallel()

	mock := &renewLoopMockJobBroker{
		renewFunc: func(_ context.Context, _ string, _ string, _ time.Duration) (job.RenewLeaseResult, error) {
			return job.RenewLeaseResult{State: job.LeaseStateCancelRequested}, nil
		},
	}

	// Build a real Worker on the mock broker so the renew-loop is
	// wired through the canonical receiver.
	w := NewWorker(WorkerDeps{
		ID:         "renew-loop-cancel-test-" + t.Name(),
		Repo:       mock,
		Dispatcher: nil, // dispatcher unused; the handler will be replaced with a no-op below
		Notifier:   nil, // notifier unused
		Log:        zap.NewNop(),
		LeaseTTL:   15 * time.Millisecond, // leaseTTL — small so renewTTL/3 = 5ms tick
		PollEvery:  1 * time.Millisecond,  // pollEvery — unused in the renew loop
		Backoff:    BackoffConfig{},
		Types:      nil,
	})

	jobCtx, jobCancel := context.WithCancel(context.Background())
	defer jobCancel()

	stopLease := make(chan struct{})
	leaseDone := make(chan struct{})
	go w.renewLeaseLoopWith(jobCtx, "test-job-id", stopLease, leaseDone, renewLeaseLoopOpts{jobCancel: jobCancel})

	// Wait for the loop to exit (the typed signal triggers immediate exit).
	select {
	case <-leaseDone:
		// good
	case <-time.After(2 * time.Second):
		close(stopLease)
		<-leaseDone
		t.Fatalf("renewLeaseLoopWith did not exit on LeaseStateCancelRequested; renewHits=%d", mock.hits())
	}

	// jobCancel MUST have been invoked — handler's ctx is now Done.
	select {
	case <-jobCtx.Done():
		// good
	default:
		t.Fatal("LeaseStateCancelRequested: jobCtx was NOT cancelled (jobCancel was not invoked)")
	}
	assert.GreaterOrEqual(t, mock.hits(), 1,
		"renewLeaseLoopWith must call RenewLease at least once before exit on CancelRequested")
}

// ── Test 2: LeaseStateLeaseLost aborts the loop (no cancel) ────────

// TestRenewLeaseLoopWith_LeaseLost_AbortsLoop pins the FASE 4(b)
// orphan-detection path: when the broker's RenewLease returns
// LeaseStateLeaseLost (the lease was stolen, expired, or reaped),
// the renew-loop MUST exit WITHOUT invoking jobCancel (the worker
// is orphaned, not cancelled). Pre-FASE-4 the equivalent signal
// was the boolean err = ErrLeaseLost return; FASE 4(b) folds it
// into the typed envelope so callers can disambiguate Lost from
// CancelRequested.
func TestRenewLeaseLoopWith_LeaseLost_AbortsLoop(t *testing.T) {
	t.Parallel()

	mock := &renewLoopMockJobBroker{
		renewFunc: func(_ context.Context, _ string, _ string, _ time.Duration) (job.RenewLeaseResult, error) {
			return job.RenewLeaseResult{State: job.LeaseStateLeaseLost}, nil
		},
	}

	w := NewWorker(WorkerDeps{
		ID:         "renew-loop-leaselost-test-" + t.Name(),
		Repo:       mock,
		Dispatcher: nil,
		Notifier:   nil,
		Log:        zap.NewNop(),
		LeaseTTL:   15 * time.Millisecond,
		PollEvery:  1 * time.Millisecond,
		Backoff:    BackoffConfig{},
		Types:      nil,
	})

	jobCtx, jobCancel := context.WithCancel(context.Background())
	defer jobCancel()

	stopLease := make(chan struct{})
	leaseDone := make(chan struct{})
	go w.renewLeaseLoopWith(jobCtx, "test-job-id", stopLease, leaseDone, renewLeaseLoopOpts{jobCancel: jobCancel})

	select {
	case <-leaseDone:
		// good
	case <-time.After(2 * time.Second):
		close(stopLease)
		<-leaseDone
		t.Fatalf("renewLeaseLoopWith did not exit on LeaseStateLeaseLost; renewHits=%d", mock.hits())
	}

	// jobCancel MUST NOT have been invoked — worker is orphaned,
	// not cancelled. ctx.Err() should still be nil.
	assert.Nil(t, jobCtx.Err(),
		"LeaseStateLeaseLost: jobCtx must NOT be cancelled (worker is orphaned, not cancelled)")
}

// TestAttemptLeaseRenewal_CancelledTerminalStateCancelsHandler pins the
// operator-cancel path. SQLite clears the lease fence when Cancel transitions
// a running job to CANCELLED, so the renewal returns LeaseLost rather than
// CancelRequested. The worker must distinguish that terminal cancellation
// from a genuinely stolen lease and route it through the cancellation state.
func TestAttemptLeaseRenewal_CancelledTerminalStateCancelsHandler(t *testing.T) {
	mock := &renewLoopMockJobBroker{
		renewFunc: func(_ context.Context, _ string, _ string, _ time.Duration) (job.RenewLeaseResult, error) {
			return job.RenewLeaseResult{State: job.LeaseStateLeaseLost}, nil
		},
		getJob: &job.Job{Status: job.StatusCancelled},
	}
	w := NewWorker(WorkerDeps{
		ID:         "cancelled-terminal-renew-test",
		Repo:       mock,
		Dispatcher: nil,
		Log:        zap.NewNop(),
		LeaseTTL:   time.Second,
	})

	result, shouldExit := w.attemptLeaseRenewal(context.Background(), "cancelled-job")
	if result.State != job.LeaseStateCancelRequested {
		t.Fatalf("state = %q, want %q", result.State, job.LeaseStateCancelRequested)
	}
	if !shouldExit {
		t.Fatal("cancelled terminal job must stop the handler")
	}
}

// ── Test 3: LeaseStateContinue is a no-op ─────────────────────────

// TestRenewLeaseLoopWith_Continue_NoOp pins the happy-path: when
// the broker's RenewLease returns LeaseStateContinue (lease extended
// successfully), the renew-loop continues ticking without invoking
// jobCancel. The test asserts the loop has NOT exited after a
// reasonable time budget (allowing ≥3 ticks at the 5ms tick
// cadence).
func TestRenewLeaseLoopWith_Continue_NoOp(t *testing.T) {
	t.Parallel()

	mock := &renewLoopMockJobBroker{
		renewFunc: func(_ context.Context, _ string, _ string, _ time.Duration) (job.RenewLeaseResult, error) {
			expiry := time.Now().Add(5 * time.Minute)
			return job.RenewLeaseResult{
				State:          job.LeaseStateContinue,
				NewLeaseExpiry: &expiry,
			}, nil
		},
	}

	w := NewWorker(WorkerDeps{
		ID:         "renew-loop-continue-test-" + t.Name(),
		Repo:       mock,
		Dispatcher: nil,
		Notifier:   nil,
		Log:        zap.NewNop(),
		LeaseTTL:   15 * time.Millisecond,
		PollEvery:  1 * time.Millisecond,
		Backoff:    BackoffConfig{},
		Types:      nil,
	})

	jobCtx, jobCancel := context.WithCancel(context.Background())
	defer jobCancel()

	stopLease := make(chan struct{})
	leaseDone := make(chan struct{})
	go w.renewLeaseLoopWith(jobCtx, "test-job-id", stopLease, leaseDone, renewLeaseLoopOpts{jobCancel: jobCancel})

	// Sleep long enough for at least 3 renew ticks (15ms / 3 = 5ms tick → 25ms covers 5 ticks).
	time.Sleep(25 * time.Millisecond)

	// The loop MUST still be alive (no exit) and jobCancel MUST NOT have been called.
	select {
	case <-leaseDone:
		t.Fatal("renewLeaseLoopWith exited on LeaseStateContinue — should be a no-op")
	default:
		// good — still alive
	}
	assert.Nil(t, jobCtx.Err(),
		"LeaseStateContinue: jobCtx must remain active (no cancel)")
	assert.GreaterOrEqual(t, mock.hits(), 1,
		"renewLeaseLoopWith must call RenewLease at least once during the test window")

	// Clean shutdown.
	close(stopLease)
	<-leaseDone
}

// ── End-to-end through Worker.runJob ─────────────────────────────

// mockCancelBroker implements job.JobBroker for the e2e cancel-signal
// test. RenewLease returns the configured state per call (typically
// CancelRequested on the 2nd tick) so the renewLeaseLoopWith observes
// the typed cancel signal and cancels jobCtx. All other repo methods
// are no-ops; finalization paths (ScheduleRetry / Fail / etc.) succeed
// but carry no assertions — the load-bearing signal is the handler
// observing ctx.Done() within the test budget.
//
// FASE 4(b): the pre-FASE-4 mock returned StatusCancelled on Get() so
// the polling IsCancelled watcher would observe the cancel state.
// The post-FASE-4 mock does NOT touch Get(); the cancel signal
// flows through RenewLease returning CancelRequested instead.
type mockCancelBroker struct {
	mu              sync.Mutex
	renewState      job.LeaseState
	renewCalls      int
	cancelAfterCall int // 0 = never; >0 = on the Nth call, set renewState to CancelRequested
	progressCalls   int
	eventCalls      int
	finalizeOp      string // last finalize mutation observed
	completeRev     int
	getCalls        int
	lastGetRev      int
	jobStatus       job.Status
	revision        int
}

func newMockCancelBroker() *mockCancelBroker {
	return &mockCancelBroker{jobStatus: job.StatusRunning, renewState: job.LeaseStateContinue}
}

func (m *mockCancelBroker) Create(_ context.Context, _ *job.Job) error { return nil }
func (m *mockCancelBroker) Get(_ context.Context, id string) (*job.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getCalls++
	m.lastGetRev = m.revision
	return &job.Job{
		ID:         id,
		Type:       TypeScriptGenerate,
		Status:     m.jobStatus,
		Revision:   m.revision,
		MaxRetries: 2,
	}, nil
}
func (m *mockCancelBroker) FindActiveByKey(_ context.Context, _ string) (*job.Job, error) {
	return nil, nil
}
func (m *mockCancelBroker) FindByTypeAndCorrelation(_ context.Context, _ string, _ string) (*job.Job, error) {
	return nil, nil
}
func (m *mockCancelBroker) FindByClientAndIdempotencyKey(_ context.Context, _, _ string) (*job.Job, error) {
	return nil, nil
}
func (m *mockCancelBroker) SetProgress(_ context.Context, _ string, p int, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.progressCalls++
	return nil
}
func (m *mockCancelBroker) AddEvent(_ context.Context, _ string, _ string, _ string, _ map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.eventCalls++
	return nil
}
func (m *mockCancelBroker) List(_ context.Context, _ job.Filter) ([]job.Job, error) {
	return nil, nil
}
func (m *mockCancelBroker) Cancel(_ context.Context, _ string) error { return nil }
func (m *mockCancelBroker) Retry(_ context.Context, _ string) (*job.Job, error) {
	return nil, nil
}
func (m *mockCancelBroker) ClaimNext(_ context.Context, _ string, _ time.Duration, _ []string) (*job.Job, error) {
	return nil, nil
}
func (m *mockCancelBroker) ListEvents(_ context.Context, _ string) ([]job.Event, error) {
	return nil, nil
}

// RenewLease returns the configured renewState; the cancelAfterCall
// knob flips the state to CancelRequested on the Nth call so the
// e2e test can observe a delayed-cancel scenario.
func (m *mockCancelBroker) RenewLease(_ context.Context, _ string, _ string, _ time.Duration) (job.RenewLeaseResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.renewCalls++
	state := m.renewState
	if m.cancelAfterCall > 0 && m.renewCalls >= m.cancelAfterCall {
		state = job.LeaseStateCancelRequested
	}
	if state == job.LeaseStateContinue {
		expiry := time.Now().Add(5 * time.Minute)
		return job.RenewLeaseResult{State: state, NewLeaseExpiry: &expiry}, nil
	}
	return job.RenewLeaseResult{State: state}, nil
}

// finalize handlers — record which branch runJob took. Asserting
// these is optional; the load-bearing signal is the handler observing
// ctx.Err() within the test budget.
func (m *mockCancelBroker) ScheduleRetry(_ context.Context, _ string, _ string, _ string, _ int, _ string, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finalizeOp = "ScheduleRetry"
	return nil
}
func (m *mockCancelBroker) Fail(_ context.Context, _ string, _ string, _ string, _ int, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finalizeOp = "Fail"
	return nil
}
func (m *mockCancelBroker) DeadLetter(_ context.Context, _ string, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finalizeOp = "DeadLetter"
	return nil
}
func (m *mockCancelBroker) Complete(_ context.Context, _ string, _ string, _ string, expectedRevision int, _ json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finalizeOp = "Complete"
	m.completeRev = expectedRevision
	return nil
}
func (m *mockCancelBroker) FinalizeAttempt(_ context.Context, _ job.FinalizeAttemptCommand) (job.FinalizeAttemptResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finalizeOp = "FinalizeAttempt"
	return job.FinalizeAttemptResult{}, nil
}

// TestWorker_CancelsRunningJobOnCancelSignal is the canonical FASE 4(b)
// end-to-end pin. The mockBroker's RenewLease returns
// LeaseStateCancelRequested on the 2nd call (cancelAfterCall=2) so the
// renewLeaseLoopWith observes the typed cancel signal on the second
// tick and invokes jobCancel. The handler blocks on ctx.Done() to
// observe the cancellation; the assertion is that the handler saw
// ctx.Err() == context.Canceled within the 4-second budget AND the
// renew-loop fired at least once.
//
// Time budget: the e2e test uses a real Worker with a small leaseTTL
// (60ms → 20ms tick); the 2nd renew call lands within ~25ms of the
// 1st call. The 4-second budget gives generous slack for test
// machine scheduling.
func TestWorker_CancelsRunningJobOnCancelSignal(t *testing.T) {
	t.Parallel()

	broker := newMockCancelBroker()
	broker.cancelAfterCall = 2 // 1st call: Continue; 2nd call: CancelRequested

	var (
		handlerSawCancel atomic.Bool
		handlerSawErr    atomic.Value
	)

	handler := HandlerFunc(func(ctx context.Context, j *job.Job, tools *JobTools) (map[string]any, error) {
		select {
		case <-ctx.Done():
			handlerSawCancel.Store(true)
			errSentinel := ctx.Err()
			handlerSawErr.Store(errSentinel)
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
			handlerSawCancel.Store(false)
			return nil, assert.AnError
		}
	})

	dispatcher := NewDispatcher()
	if err := dispatcher.Register(TypeScriptGenerate, handler); err != nil {
		t.Fatalf("register handler: %v", err)
	}

	worker := NewWorker(WorkerDeps{
		ID:         "cancellation-test-worker",
		Repo:       broker,
		Dispatcher: dispatcher,
		Notifier:   nil, // notifier unused in runJob's cancel path
		Log:        zap.NewNop(),
		LeaseTTL:   60 * time.Millisecond, // leaseTTL — small for fast renew tick (60/3 = 20ms)
		PollEvery:  2 * time.Second,       // pollEvery
		Backoff: BackoffConfig{
			MaxBackoff:                30 * time.Second,
			JitterFraction:            0,
			ConsecutiveEmptyThreshold: 0,
		},
		Types: []string{TypeScriptGenerate},
	})

	parentCtx, parentCancel := context.WithCancel(context.Background())
	defer parentCancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.runJob(parentCtx, &job.Job{
			ID:         "test-job-id",
			Type:       TypeScriptGenerate,
			Status:     job.StatusRunning,
			MaxRetries: 2,
			RetryCount: 0,
		})
	}()

	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatalf("runJob did not complete within 8s; handlerSawCancel=%v broker=%+v",
			handlerSawCancel.Load(), broker.finalizeOp)
	}

	assert.True(t, handlerSawCancel.Load(),
		"FASE 4(b): handler must observe ctx.Done() (the typed LeaseStateCancelRequested signal propagated into jobCtx)")
	capturedErr, _ := handlerSawErr.Load().(error)
	assert.Equal(t, context.Canceled, capturedErr,
		"handler must see ctx.Err() == context.Canceled (not DeadlineExceeded, etc.)")

	broker.mu.Lock()
	defer broker.mu.Unlock()
	assert.GreaterOrEqual(t, broker.renewCalls, 1,
		"FASE 4(b): the renew-loop must have called RenewLease at least once before observing CancelRequested (otherwise the typed signal could not have propagated)")
	assert.NotEmpty(t, broker.finalizeOp,
		"worker finalisation should have been called (Cancel / Fail / Complete / ScheduleRetry / DeadLetter)")
}

// Regression: the worker must finalise with the latest DB revision
// after the lease loop stops, not the stale claim-time snapshot.
// FASE 4(b): unchanged from the pre-FASE-4 contract (finalizer uses
// the latest DB revision, not the claim-time snapshot).
func TestWorker_UsesCurrentRevisionAtFinalization(t *testing.T) {
	t.Parallel()

	broker := newMockCancelBroker()
	broker.jobStatus = job.StatusRunning
	broker.revision = 7
	// renewState stays LeaseStateContinue — the worker completes
	// the happy path without cancellation.

	handler := HandlerFunc(func(ctx context.Context, j *job.Job, tools *JobTools) (map[string]any, error) {
		return map[string]any{"ok": true}, nil
	})

	dispatcher := NewDispatcher()
	if err := dispatcher.Register(TypeScriptGenerate, handler); err != nil {
		t.Fatalf("register handler: %v", err)
	}

	worker := NewWorker(WorkerDeps{
		ID:         "revision-test-worker",
		Repo:       broker,
		Dispatcher: dispatcher,
		Notifier:   nil,
		Log:        zap.NewNop(),
		LeaseTTL:   5 * time.Minute,
		PollEvery:  2 * time.Second,
		Backoff: BackoffConfig{
			MaxBackoff:                30 * time.Second,
			JitterFraction:            0,
			ConsecutiveEmptyThreshold: 0,
		},
		Types: []string{TypeScriptGenerate},
	})

	parentCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.runJob(parentCtx, &job.Job{
			ID:         "revision-test-job",
			Type:       TypeScriptGenerate,
			Status:     job.StatusRunning,
			WorkerID:   "worker-1",
			LeaseID:    "lease-1",
			Revision:   1,
			MaxRetries: 2,
		})
	}()

	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("runJob did not complete")
	}

	broker.mu.Lock()
	defer broker.mu.Unlock()
	if broker.finalizeOp != "Complete" {
		t.Fatalf("finalizeOp = %q, want Complete", broker.finalizeOp)
	}
	if broker.getCalls == 0 {
		t.Fatal("expected runJob to read the current job revision via Get before finalization")
	}
	if broker.lastGetRev != 7 {
		t.Fatalf("Get saw revision = %d, want 7", broker.lastGetRev)
	}
	if broker.completeRev != 7 {
		t.Fatalf("Complete expectedRevision = %d, want 7", broker.completeRev)
	}
}

// ── FASE 6 Cut 6.5 typed JobCompletionBus test lives in ───────────
//
// internal/capabilities/jobs/policy/job_finalizer_e2e_test.go
// (the canonical Finalizer e2e surface). It cannot live in
// cancellation_test.go because the package-level imports of
// `internal/capabilities/jobs/queue` form a closed cycle when
// cancellation_test.go imports finalizer + completion. The split
// honors godlike/06 SSOT (one canonical owner per fact) and the
// codebase's reuse rule (existing e2e helpers in finalizer_e2e_test.go
// are reused; zero new helper code was introduced). See
// TestFinalizer_PublishesTypedJobCompletionEvent_PostFlipRevision
// + TestFinalizer_NoPublishOnFailure along with the canonical
// "no per-job polling cycles" goal that FASE 6 (Cut 6.3 worker
// typed-LeaseState + Cut 6.5 typed-JobCompletionBus) establishes.
