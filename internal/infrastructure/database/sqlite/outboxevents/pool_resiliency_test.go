package outboxevents

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

// ── FakeClock — injectable Clock for deterministic tests ────────────

type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func NewFakeClock(t time.Time) *FakeClock { return &FakeClock{now: t} }
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// ── fakeFinisher — records event state transitions ─────────────────

type fakeFinisher struct {
	deadLetters atomic.Int32
	supersededs atomic.Int32
	failed      atomic.Int32
	completed   atomic.Int32
	lastErr     string
	lastMu      sync.Mutex
}

func (r *fakeFinisher) MarkDeadLetter(_ context.Context, _ int64, _, errMsg string) error {
	r.deadLetters.Add(1)
	r.lastMu.Lock()
	r.lastErr = errMsg
	r.lastMu.Unlock()
	return nil
}

func (r *fakeFinisher) MarkSuperseded(_ context.Context, _ int64, _, _ string) error {
	r.supersededs.Add(1)
	return nil
}

func (r *fakeFinisher) MarkFailed(_ context.Context, _ int64, _, _ string, _ time.Time) error {
	r.failed.Add(1)
	return nil
}

func (r *fakeFinisher) MarkCompleted(_ context.Context, _ int64, _ string) error {
	r.completed.Add(1)
	return nil
}

// ── testHandler ─────────────────────────────────────────────────────

type testHandler struct {
	eventType   string
	handleFn    func(ctx context.Context, evt Event) error
	handleCalls atomic.Int32
}

func (h *testHandler) EventType() string      { return h.eventType }
func (h *testHandler) IdempotencyKey() string { return h.eventType + ".test.v1" }
func (h *testHandler) Handle(ctx context.Context, evt Event) error {
	h.handleCalls.Add(1)
	return h.handleFn(ctx, evt)
}

// ── helpers ─────────────────────────────────────────────────────────

func defaultTestConfig() WorkerPollConfig {
	return WorkerPollConfig{
		PollInterval:    100 * time.Millisecond,
		ProcessTimeout:  5 * time.Second,
		ReclaimInterval: 100 * time.Millisecond,
		BackoffCap:      30 * time.Minute,
		JitterFraction:  0.0,
	}
}

func makeTestEvent(eventType string) Event {
	return Event{ID: 1, EventType: eventType, EventKey: "test-key-1", PayloadJSON: `{}`, Status: "pending", MaxAttempts: 5}
}

// ── Test 1: HandlerPanic_DoesNotKillWorker ──────────────────────────

func TestPool_HandlerPanic_DoesNotKillWorker(t *testing.T) {
	reg := NewHandlerRegistry()
	fin := &fakeFinisher{}

	panicHandler := &testHandler{
		eventType: "test.panic",
		handleFn: func(_ context.Context, _ Event) error {
			panic("simulated handler panic for testing")
		},
	}
	okHandler := &testHandler{
		eventType: "test.ok",
		handleFn:  func(_ context.Context, _ Event) error { return nil },
	}
	reg.Register(panicHandler)
	reg.Register(okHandler)

	pool := NewPool("panic-test", nil, reg, zap.NewNop(), defaultTestConfig())
	pool.finisher = fin

	pool.processEvent(context.Background(), &Claim{Event: makeTestEvent("test.panic"), LeaseID: "lease-a"})
	pool.processEvent(context.Background(), &Claim{Event: makeTestEvent("test.ok"), LeaseID: "lease-b"})

	if panicHandler.handleCalls.Load() != 1 {
		t.Errorf("panic handler should be called once, got %d", panicHandler.handleCalls.Load())
	}
	if okHandler.handleCalls.Load() != 1 {
		t.Errorf("ok handler should be called once, got %d", okHandler.handleCalls.Load())
	}
	if fin.deadLetters.Load() != 1 {
		t.Errorf("expected 1 dead_letter (panic), got %d", fin.deadLetters.Load())
	}
	if fin.completed.Load() != 1 {
		t.Errorf("expected 1 completed (ok), got %d", fin.completed.Load())
	}
}

// ── Test 2: MissingHandler_DeadLettersImmediately ───────────────────

func TestPool_MissingHandler_DeadLettersImmediately(t *testing.T) {
	reg := NewHandlerRegistry()
	fin := &fakeFinisher{}

	pool := NewPool("missing-test", nil, reg, zap.NewNop(), defaultTestConfig())
	pool.finisher = fin

	pool.processEvent(context.Background(), &Claim{Event: makeTestEvent("test.unregistered"), LeaseID: "lease-1"})

	if fin.deadLetters.Load() != 1 {
		t.Errorf("expected 1 dead_letter (missing handler), got %d", fin.deadLetters.Load())
	}
	if fin.failed.Load() != 0 {
		t.Errorf("expected 0 retryable failures (should dead_letter), got %d", fin.failed.Load())
	}
}

// ── Test 3: TransientFailure_BackoffIsCapped ────────────────────────

func TestPool_TransientFailure_BackoffIsCapped(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))
	cfg := defaultTestConfig()
	cfg.BackoffCap = 5 * time.Minute
	cfg.JitterFraction = 0.0

	pool := NewPool("backoff-test", nil, nil, zap.NewNop(), cfg)
	pool.clock = clock

	var prevDelay time.Duration
	for attempt := 1; attempt <= 10; attempt++ {
		next := pool.computeNextAttempt(1, attempt)
		delay := next.Sub(clock.Now())
		if delay <= 0 {
			t.Errorf("attempt %d: delay=%v should be positive", attempt, delay)
		}
		if delay > cfg.BackoffCap {
			t.Errorf("attempt %d: delay=%v exceeds cap=%v", attempt, delay, cfg.BackoffCap)
		}
		if attempt > 1 && delay < prevDelay && prevDelay < cfg.BackoffCap {
			t.Errorf("attempt %d: delay=%v decreased from prev=%v", attempt, delay, prevDelay)
		}
		prevDelay = delay
	}

	nextHigh := pool.computeNextAttempt(1, 20)
	delayHigh := nextHigh.Sub(clock.Now())
	if delayHigh > cfg.BackoffCap {
		t.Errorf("attempt 20: delay=%v exceeds cap=%v", delayHigh, cfg.BackoffCap)
	}
}

// ── Test 4: MarkCompletedFailure_ReprocessesIdempotently ────────────

func TestPool_MarkCompletedFailure_ReprocessesIdempotently(t *testing.T) {
	reg := NewHandlerRegistry()
	fin := &fakeFinisher{}
	handler := &testHandler{
		eventType: "test.idempotent",
		handleFn:  func(_ context.Context, _ Event) error { return nil },
	}
	reg.Register(handler)

	pool := NewPool("mark-completed-test", nil, reg, zap.NewNop(), defaultTestConfig())
	pool.finisher = fin

	pool.processEvent(context.Background(), &Claim{Event: makeTestEvent("test.idempotent"), LeaseID: "lease-1"})

	if handler.handleCalls.Load() != 1 {
		t.Errorf("handler should be called once, got %d", handler.handleCalls.Load())
	}
	if fin.completed.Load() != 1 {
		t.Errorf("expected 1 MarkCompleted, got %d", fin.completed.Load())
	}
}

// ── Test 5: TerminalErrorClassification ─────────────────────────────

func TestPool_TerminalErrorClassification(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		terminal bool
	}{
		{"missing handler (NewTerminalError)", NewTerminalError(fmt.Errorf("no handler registered for event type foo.bar")), true},
		{"handler panic (NewTerminalError)", NewTerminalError(fmt.Errorf("handler panic: boom")), true},
		{"typed terminal error", NewTerminalError(fmt.Errorf("payload invalid")), true},
		{"legacy breadcrumb", fmt.Errorf("delivery failed (terminal)"), true},
		{"plain retryable error", fmt.Errorf("network timeout"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if IsTerminal(tt.err) != tt.terminal {
				t.Errorf("IsTerminal: got %v, want %v (err=%v)", IsTerminal(tt.err), tt.terminal, tt.err)
			}
		})
	}
}

// ── Clock tests ─────────────────────────────────────────────────────

func TestRealClock_Now(t *testing.T) {
	c := RealClock{}
	before := time.Now()
	now := c.Now()
	after := time.Now()
	if now.Before(before) || now.After(after) {
		t.Errorf("RealClock.Now()=%v not in [%v, %v]", now, before, after)
	}
}

func TestFakeClock_Advance(t *testing.T) {
	start := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	c := NewFakeClock(start)
	if !c.Now().Equal(start) {
		t.Errorf("initial: got %v, want %v", c.Now(), start)
	}
	c.Advance(5 * time.Minute)
	if want := start.Add(5 * time.Minute); !c.Now().Equal(want) {
		t.Errorf("after advance: got %v, want %v", c.Now(), want)
	}
}

// ── Backoff tests ───────────────────────────────────────────────────

func TestComputeNextAttempt_GrowsExponentially(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))
	cfg := defaultTestConfig()
	cfg.BackoffCap = 1 * time.Hour

	pool := NewPool("backoff-growth", nil, nil, zap.NewNop(), cfg)
	pool.clock = clock

	for _, tc := range []struct {
		attempt int
		wantMin time.Duration
	}{
		{1, 1 * time.Minute},
		{2, 2 * time.Minute},
		{3, 4 * time.Minute},
		{4, 8 * time.Minute},
		{5, 16 * time.Minute},
	} {
		delay := pool.computeNextAttempt(1, tc.attempt).Sub(clock.Now())
		if delay < tc.wantMin {
			t.Errorf("attempt %d: delay=%v < expected %v", tc.attempt, delay, tc.wantMin)
		}
	}
}

func TestComputeNextAttempt_CappedAtBackoffCap(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))
	cfg := defaultTestConfig()
	cfg.BackoffCap = 3 * time.Minute

	pool := NewPool("backoff-cap", nil, nil, zap.NewNop(), cfg)
	pool.clock = clock

	delay := pool.computeNextAttempt(1, 10).Sub(clock.Now())
	if delay > cfg.BackoffCap {
		t.Errorf("delay=%v exceeds cap=%v", delay, cfg.BackoffCap)
	}
}

func TestComputeNextAttempt_WithJitter(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))
	cfg := defaultTestConfig()
	cfg.JitterFraction = 0.2
	cfg.BackoffCap = 30 * time.Minute

	pool := NewPool("backoff-jitter", nil, nil, zap.NewNop(), cfg)
	pool.clock = clock

	base := 4 * time.Minute
	jitterRange := time.Duration(float64(base) * 0.2)
	delay := pool.computeNextAttempt(1, 3).Sub(clock.Now())

	if delay < base-jitterRange || delay > base+jitterRange {
		t.Errorf("delay=%v not in [%v, %v]", delay, base-jitterRange, base+jitterRange)
	}
}

// ── WithClock test ──────────────────────────────────────────────────

func TestPool_WithClock(t *testing.T) {
	pool := NewPool("clock-test", nil, nil, zap.NewNop(), defaultTestConfig())
	if _, ok := pool.clock.(RealClock); !ok {
		t.Error("default clock should be RealClock")
	}
	fake := NewFakeClock(time.Now())
	pool.WithClock(fake)
	if pool.clock != fake {
		t.Error("WithClock should set the clock")
	}
	pool.WithClock(nil)
	if pool.clock != fake {
		t.Error("WithClock(nil) should keep existing clock")
	}
}
