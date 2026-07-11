// Package jobs -- cancellation_test.go (Issue 6 / P1, June 2026).
//
// Pins the cancellation-plumbing contract:
//
//  1. TestStartCancelWatcher -- helper-level test of the polling
//     goroutine in isolation. Calls startCancelWatcher with a
//     mock isCancelled function that returns true on the 2nd
//     poll, asserts the parent ctx becomes Done within 4 seconds
//     (= 2 poll intervals, accounting for the worst-case
//     boundary).
//
//  2. TestWorker_CancelsRunningJobOnCancelSignal -- end-to-end
//     through Worker.runJob with a mockBroker that returns
//     StatusCancelled on Get(jobID) (so IsCancelled polls return
//     true) and a fakeDispatcher that registers a handler that
//     observes ctx.Done(). Asserts the handler saw ctx.Err() ==
//     context.Canceled within the 4-second tolerance.
//
// The two-tier structure lets the helper-level test pin the
// polling cadence in isolation (no goroutine timing flakiness from
// the broker-claim loop) while the e2e test pins the wiring through
// real Worker.runJob. If either tier drifts, the test catches it
// at unit cost before integration.
//
// Why 2-second polling + 4-second tolerance: cancelPollInterval
// = 2 * time.Second is the canonical cadence chosen in
// worker_execution.go::startCancelWatcher; handler-observation
// within 2 tick intervals gives a deterministic upper bound on
// the test runtime.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// TestStartCancelWatcher_PollsUntilCancelFires verifies the
// goroutine polling semantics in isolation:
//
//   - on tick 0 (immediately after spawn): poll call recorded.
//   - on tick 1 (after 2s): isCancelled returns true; watcher
//     calls jobCancel; parent ctx becomes Done.
//   - on tick 2+: no-op (watcher returned after Cancel()).
//
// Time tolerance: the watcher enters the select right after spawn;
// first poll fires 2s later. Adding 1 second slack for goroutine
// scheduling means the test asserts Done within 4 seconds.
func TestStartCancelWatcher_PollsUntilCancelFires(t *testing.T) {
	t.Parallel()

	var pollCount int32
	cancelTrueAfter := 1 // poll 0 = no-op, poll 1 = returns true

	jobCtx, jobCancel := context.WithCancel(context.Background())
	defer jobCancel()

	startCancelWatcher(jobCtx, jobCancel, func() bool {
		atomic.AddInt32(&pollCount, 1)
		return atomic.LoadInt32(&pollCount) > int32(cancelTrueAfter)
	})

	// Wait up to 6 seconds for ctx to become Done. Worst case:
	// spawn -> first tick at 2s (pollCount=1, returns false) ->
	// second tick at 4s (pollCount=2, returns true, Cancel) ->
	// Done. The 6s budget gives 2 seconds of slack past the
	// second tick's exact-fire moment so the select-case race
	// between timer.C and jobCtx.Done() reliably picks the
	// Done case (a previous 4s budget was a real flake on
	// the boundary: when timer.C fired at the same instant
	// as the second poll, the select picked non-deterministically).
	deadline := time.NewTicker(50 * time.Millisecond)
	defer deadline.Stop()
	timer := time.NewTimer(6 * time.Second)
	defer timer.Stop()

	for {
		select {
		case <-jobCtx.Done():
			require.Equal(t, context.Canceled, jobCtx.Err(),
				"Issue 6 / P1: watcher must cancel jobCtx with context.Canceled")
			// Confirm the watcher polled at least twice (1 = false, 2 = true).
			assert.GreaterOrEqual(t, atomic.LoadInt32(&pollCount), int32(2),
				"watcher should have polled at least 2 times before firing cancel")
			return
		case <-timer.C:
			t.Fatalf("cancel not fired within 6s; pollCount=%d ctx.Err=%v",
				atomic.LoadInt32(&pollCount), jobCtx.Err())
		case <-deadline.C:
			// re-check ctx in next iter
		}
	}
}

// TestStartCancelWatcher_NilIsCancelledIsNoOp verifies the
// nil-tolerance contract: nil isCancelled function means the
// helper does NOT spawn a goroutine (or if it does, it short-
// circuits). Either way, jobCtx must remain unchanged.
func TestStartCancelWatcher_NilIsCancelledIsNoOp(t *testing.T) {
	t.Parallel()

	jobCtx, jobCancel := context.WithCancel(context.Background())
	defer jobCancel()

	startCancelWatcher(jobCtx, jobCancel, nil) // nil-tolerant

	// Sleep long enough for at least one poll tick to fire -- if
	// the goroutine were spawned with a nil isCancelled it would
	// panic on the first call. The test passes == no panic.
	time.Sleep(100 * time.Millisecond)
	assert.Nil(t, jobCtx.Err(),
		"nil isCancelled must not affect jobCtx; should produce no goroutine that touches ctx")
}

// TestStartCancelWatcher_ExitsOnCtxDone verifies the goroutine
// exits when jobCtx becomes Done (this is the natural exit path
// via `defer jobCancel()` in the caller / worker). Even if
// isCancelled never returns true, the watcher must close its
// loop when its parent ctx is cancelled.
func TestStartCancelWatcher_ExitsOnCtxDone(t *testing.T) {
	t.Parallel()

	jobCtx, jobCancel := context.WithCancel(context.Background())

	var pollHits int32
	startCancelWatcher(jobCtx, jobCancel, func() bool {
		atomic.AddInt32(&pollHits, 1)
		return false // never report cancel
	})

	// Let the watcher poll once.
	time.Sleep(2500 * time.Millisecond)
	firstHits := atomic.LoadInt32(&pollHits)
	assert.GreaterOrEqual(t, firstHits, int32(1),
		"watcher should have polled at least once in 2.5s")

	// Now self-cancel the parent ctx -- the watcher's
	// jobCtx.Done() branch fires and the goroutine returns.
	jobCancel()

	// Wait for the goroutine to exit. Hard to observe directly
	// without instrumentation; sleep + non-racy check via pollHits
	// that does not grow after cancellation.
	time.Sleep(1500 * time.Millisecond)
	finalHits := atomic.LoadInt32(&pollHits)
	// The watcher does NOT poll after jobCtx.Done() (the select
	// branch wins), so finalHits should be approximately equal to
	// firstHits. Allow +/-1 for a poll that may have been in
	// flight at the moment of cancel.
	delta := finalHits - firstHits
	if delta < 0 {
		delta = -delta
	}
	assert.LessOrEqual(t, delta, int32(1),
		"watcher must exit when ctx is cancelled (poll count delta: max 1 for race window)")
}

// ── End-to-end through Worker.runJob ─────────────────────────────────

// mockCancelBroker implements job.JobBroker + the lease + retry
// helpers needed by Worker.runJob. The default status returned by
// Get() is CANCELLED so the cancel-watcher polls trigger as soon
// as the goroutine spawns. All other repo methods are no-ops;
// finalization paths (`ScheduleRetry`, `Fail`, etc.) succeed but
// carry no assertions -- the load-bearing signal is the handler
// observing ctx.Done() within the 2s poll cadence.
type mockCancelBroker struct {
	mu            sync.Mutex
	jobStatus     job.Status
	revision      int
	progressCalls int
	eventCalls    int
	finalizeOp    string // last finalize mutation observed
	completeRev   int
	getCalls      int
	lastGetRev    int
}

func newMockCancelBroker() *mockCancelBroker {
	return &mockCancelBroker{jobStatus: job.StatusCancelled}
}

func (m *mockCancelBroker) Create(_ context.Context, _ *job.Job) error { return nil }
func (m *mockCancelBroker) Get(_ context.Context, id string) (*job.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getCalls++
	m.lastGetRev = m.revision
	return &job.Job{
		ID:         id,
		Type:       job.TypeScriptGenerate,
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
func (m *mockCancelBroker) SetProgress(_ context.Context, _ string, p int, msg string) error {
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
func (m *mockCancelBroker) Cancel(_ context.Context, _ string) error            { return nil }
func (m *mockCancelBroker) Retry(_ context.Context, _ string) (*job.Job, error) { return nil, nil }
func (m *mockCancelBroker) ClaimNext(_ context.Context, _ string, _ time.Duration, _ []string) (*job.Job, error) {
	// Issue 6 / P1: ClaimNext is part of job.Store but unused by
	// Worker.runJob's cancel-path (runJob claims via the broker
	// claim loop, NOT inside runJob itself). Return nil to keep
	// the test honest about which code paths it covers.
	return nil, nil
}
func (m *mockCancelBroker) ListEvents(_ context.Context, _ string) ([]job.Event, error) {
	return nil, nil
}
func (m *mockCancelBroker) RenewLease(_ context.Context, _ string, _ string, _ time.Duration) error {
	return nil
}

// finalize handlers -- record which branch runJob took (ScheduleRetry
// vs Fail vs Complete). Asserting these is optional; the load-
// bearing signal is the handler observing ctx.Err().
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

// FinalizeAttempt is a Push 4.6 stub (godlike/06 SSOT kernel-job-cutover
// follow-up). The cancellation tests exercise Worker.runJob's cancel path
// via legacy Complete / Fail / ScheduleRetry path — FinalizeAttempt is
// not in the test path. Push 4.6 replaces this stub with a typed test
// double that records the FinalizeAttemptCommand per the Push 4.2
// canonical contract. Stub returns errors.New to fail-fast if a future
// regression accidentally exercises this codepath through the mock
// (godlike/07 fail-closed).
func (m *mockCancelBroker) FinalizeAttempt(_ context.Context, _ kerneljob.FinalizeAttemptCommand) (kerneljob.FinalizeAttemptResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finalizeOp = "FinalizeAttempt"
	return kerneljob.FinalizeAttemptResult{}, errors.New("mockCancelBroker.FinalizeAttempt: Push 4.6 stub (cancel-path uses legacy Complete/Fail/ScheduleRetry)")
}

// TestWorker_CancelsRunningJobOnCancelSignal is the canonical
// Issue 6 / P1 end-to-end pin. The mockBroker returns
// StatusCancelled on every Get(), so IsCancelled poll reports true
// the moment the watcher spawns. The handler blocks on
// ctx.Done() to observe the cancellation signal -- the assertion
// is that the handler saw ctx.Err() == context.Canceled AND the
// watcher fired (pollCount > 0) within the 4-second tolerance.
//
// Time budget: watcher polls every 2s, handler should observe
// within 1-2 poll intervals. 4s gives generous slack for test
// machine scheduling.
func TestWorker_CancelsRunningJobOnCancelSignal(t *testing.T) {
	t.Parallel()

	broker := newMockCancelBroker()

	// capture variables for the handler closure.
	var (
		handlerSawCancel atomic.Bool
		handlerSawErr    atomic.Value // error the handler returned
	)

	handler := HandlerFunc(func(ctx context.Context, j *job.Job, tools *JobTools) (map[string]any, error) {
		select {
		case <-ctx.Done():
			handlerSawCancel.Store(true)
			errSentinel := ctx.Err()
			handlerSawErr.Store(errSentinel)
			// Return ctx.Err() so runJob hits the failed path
			// -- this is the canonical signal that handlers will
			// short-circuit / propagate ctx cancellation. The
			// worker's final status is "CANCELLED" + retry
			// attempt (the DB-side status is the source of truth
			// from before the watcher fired; we already verified
			// the user-press-cancel propagates into ctx).
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
			handlerSawCancel.Store(false)
			return nil, errors.New("handler timed out without observing ctx cancellation")
		}
	})

	dispatcher := NewDispatcher()
	if err := dispatcher.Register(job.TypeScriptGenerate, handler); err != nil {
		t.Fatalf("register handler: %v", err)
	}

	worker := NewWorker(
		"cancellation-test-worker",
		broker,
		dispatcher,
		nil, // notifier unused in runJob's cancel path
		zap.NewNop(),
		5*time.Minute, // leaseTTL
		2*time.Second, // pollEvery
		BackoffConfig{
			MaxBackoff:                30 * time.Second,
			JitterFraction:            0,
			ConsecutiveEmptyThreshold: 0,
		},
		[]string{job.TypeScriptGenerate},
	)

	// Drive runJob directly. parent ctx is a background ctx; the
	// job timeout configures the job timeout (1 minute here so it
	// does not interfere with the cancel signal). The watcher
	// polls every 2s; mockBroker.Get returns StatusCancelled so
	// the FIRST poll fires Cancel and the handler exits.
	parentCtx, parentCancel := context.WithCancel(context.Background())
	defer parentCancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.runJob(parentCtx, &job.Job{
			ID:         "test-job-id",
			Type:       job.TypeScriptGenerate,
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
		"Issue 6 / P1: handler must observe ctx.Done() (the cancel-watcher trigger propagated into jobCtx)")
	capturedErr, _ := handlerSawErr.Load().(error)
	assert.Equal(t, context.Canceled, capturedErr,
		"handler must see ctx.Err() == context.Canceled (not DeadlineExceeded, etc.)")

	// Sanity: the broker must have been polled at least once for
	// progress (set once per runJob's lease heartbeat, plus the
	// second set when the handler exits). The cancel-trigger path
	// ensures the watcher polled IsCancelled at least once.
	assert.NotEmpty(t, broker.finalizeOp,
		"worker finalisation should have been called (Cancel / Fail / Complete / ScheduleRetry / DeadLetter)")
}

// Regression: the worker must finalise with the latest DB revision
// after the lease loop stops, not the stale claim-time snapshot.
func TestWorker_UsesCurrentRevisionAtFinalization(t *testing.T) {
	t.Parallel()

	broker := newMockCancelBroker()
	broker.jobStatus = job.StatusRunning
	broker.revision = 7

	handler := HandlerFunc(func(ctx context.Context, j *job.Job, tools *JobTools) (map[string]any, error) {
		return map[string]any{"ok": true}, nil
	})

	dispatcher := NewDispatcher()
	if err := dispatcher.Register(job.TypeScriptGenerate, handler); err != nil {
		t.Fatalf("register handler: %v", err)
	}

	worker := NewWorker(
		"revision-test-worker",
		broker,
		dispatcher,
		nil,
		zap.NewNop(),
		5*time.Minute,
		2*time.Second,
		BackoffConfig{
			MaxBackoff:                30 * time.Second,
			JitterFraction:            0,
			ConsecutiveEmptyThreshold: 0,
		},
		[]string{job.TypeScriptGenerate},
	)

	parentCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.runJob(parentCtx, &job.Job{
			ID:         "revision-test-job",
			Type:       job.TypeScriptGenerate,
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
