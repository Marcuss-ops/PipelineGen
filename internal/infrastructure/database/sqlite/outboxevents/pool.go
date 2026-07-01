package outboxevents

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ── Clock — injectable time source for testability ────────────────────

// Clock is the injectable time source. The production implementation
// (RealClock) delegates to time.Now. Tests inject a FakeClock so
// backoff curves and lease-expiry paths can be driven deterministically
// without time.Sleep hacks.
type Clock interface {
	Now() time.Time
}

// RealClock delegates to time.Now. The zero value is usable.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

// ── WorkerPollConfig ──────────────────────────────────────────────────

type WorkerPollConfig struct {
	PollInterval    time.Duration
	ProcessTimeout  time.Duration
	ReclaimInterval time.Duration
	// BackoffCap is the maximum backoff delay for retryable errors.
	// Zero defaults to 30 minutes.
	BackoffCap time.Duration
	// JitterFraction is the ± fraction applied to backoff delays
	// to prevent thundering-herd retries. Zero defaults to 0.2 (±20%).
	JitterFraction float64
}

// eventFinisher is the narrow surface processEvent needs to finalize
// events. Extracted as an interface so tests can inject a fake without
// requiring a full *Repository (FASE 2.2, July 2026).
type eventFinisher interface {
	MarkDeadLetter(ctx context.Context, eventID int64, leaseID, errMsg string) error
	MarkSuperseded(ctx context.Context, eventID int64, leaseID, errMsg string) error
	MarkFailed(ctx context.Context, eventID int64, leaseID, errMsg string, nextAttemptAt time.Time) error
	MarkCompleted(ctx context.Context, eventID int64, leaseID string) error
}

type Pool struct {
	name     string
	repo     *Repository
	finisher eventFinisher
	registry *HandlerRegistry
	log      *zap.Logger
	cfg      WorkerPollConfig
	clock    Clock
	stopChan chan struct{}
	wg       sync.WaitGroup
	// stopOnce guards close(p.stopChan) so concurrent or sequential
	// calls to Stop() — across the production SafeGo("outbox-events-shutdown")
	// handler on ctx.Done() AND the SafeGo("cleanup-outbox-events-pool")
	// handler in shutdown.go::buildCleanup — cannot trigger
	// `panic: close of closed channel` (PR4.E followup commit 94853aa
	// surfaced the second Stop caller; sync.Once here closes the race).
	stopOnce sync.Once
}

func NewPool(name string, repo *Repository, registry *HandlerRegistry, log *zap.Logger, cfg WorkerPollConfig) *Pool {
	return &Pool{
		name:     name,
		repo:     repo,
		finisher: repo,
		registry: registry,
		log:      log.With(zap.String("pool", name)),
		cfg:      cfg,
		clock:    RealClock{},
		stopChan: make(chan struct{}),
	}
}

// WithClock sets an injectable Clock for testability. Returns the
// same Pool so construction can be chained. Nil clock falls back
// to RealClock.
func (p *Pool) WithClock(c Clock) *Pool {
	if c != nil {
		p.clock = c
	}
	return p
}

func (p *Pool) Start(ctx context.Context, workers int) {
	p.log.Info("starting outbox events pool", zap.Int("workers", workers))

	// Start reclaim loop
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		reclaimTicker := time.NewTicker(p.cfg.ReclaimInterval)
		defer reclaimTicker.Stop()
		for {
			select {
			case <-reclaimTicker.C:
				p.reclaim(ctx)
			case <-p.stopChan:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	// Start workers
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go func(workerID int) {
			defer p.wg.Done()
			workerName := fmt.Sprintf("%s-worker-%d", p.name, workerID)
			pollTicker := time.NewTicker(p.cfg.PollInterval)
			defer pollTicker.Stop()
			for {
				select {
				case <-pollTicker.C:
					p.pollAndProcess(ctx, workerName)
				case <-p.stopChan:
					return
				case <-ctx.Done():
					return
				}
			}
		}(i)
	}

	p.wg.Wait()
}

// Stop signals the pool to drain and waits for in-flight workers to exit.
//
// Idempotent: concurrent or sequential multiple calls are safe. The
// `close(p.stopChan)` is guarded by sync.Once — PR4.E followup commit
// 94853aa added a second SafeGo("cleanup-outbox-events-pool") caller
// in shutdown.go::buildCleanup which races with the lifecycle's
// SafeGo("outbox-events-shutdown") caller on ctx.Done(). Without
// the guard, the second close panics with `close of closed channel`.
// The p.wg.Wait() after the close is naturally idempotent — wg.Wait
// returns immediately once the count hits zero on the second call,
// so the timeout/Select arithmetic is also safe to re-enter.
func (p *Pool) Stop(timeout time.Duration) error {
	p.log.Info("stopping outbox events pool")
	p.stopOnce.Do(func() {
		close(p.stopChan)
	})

	c := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(c)
	}()

	select {
	case <-c:
		p.log.Info("outbox events pool stopped successfully")
		return nil
	case <-time.After(timeout):
		p.log.Warn("outbox events pool stop timed out")
		return fmt.Errorf("outbox events pool stop timed out")
	}
}

// computeNextAttempt returns now + jittered exponential backoff.
//
// Formula: base = min(1min * 2^attemptCount, backoffCap) with
// ±jitterFraction random jitter. The production cap is 30min.
// Jitter defaults to ±20% when cfg.JitterFraction is zero.
//
// attemptCount is 1-indexed (first retry = attemptCount 1 → base 2min).
// Exported so tests can lock the curve without deriving the formula.
func (p *Pool) computeNextAttempt(attemptCount int) time.Time {
	if attemptCount < 1 {
		attemptCount = 1
	}
	backoffCap := p.cfg.BackoffCap
	if backoffCap <= 0 {
		backoffCap = 30 * time.Minute
	}
	jitterFrac := p.cfg.JitterFraction

	// Exponential: 1min * 2^(attemptCount-1)
	base := time.Minute * time.Duration(1<<uint(attemptCount-1))

	// Apply jitter FIRST, then cap the total so the result never
	// exceeds backoffCap (Blocco 5 fix: pre-fix, jitter was added
	// after capping, causing capped+jitter to overshoot).
	var jitter time.Duration
	if jitterFrac > 0 {
		jitterRange := time.Duration(float64(base) * jitterFrac)
		// Deterministic jitter via attemptCount as seed so tests can
		// assert the range without rand.Seed races.
		r := rand.New(rand.NewSource(int64(attemptCount)))
		jitter = time.Duration(r.Int63n(int64(jitterRange*2)+1)) - jitterRange
	}

	total := base + jitter
	if total > backoffCap {
		total = backoffCap
	}
	if total < 1*time.Second {
		total = 1 * time.Second
	}

	return p.clock.Now().Add(total)
}

func (p *Pool) reclaim(ctx context.Context) {
	affected, err := p.repo.RequeueExpiredLeases(ctx)
	if err != nil {
		p.log.Error("failed to requeue expired leases", zap.Error(err))
		return
	}
	if affected > 0 {
		p.log.Info("requeued expired leases", zap.Int("count", affected))
	}
}

func (p *Pool) pollAndProcess(ctx context.Context, workerName string) {
	for {
		// Claim next event
		claim, err := p.repo.ClaimNext(ctx, workerName, p.cfg.ProcessTimeout)
		if err != nil {
			// Check if it's no rows
			// ClaimNext returns nil, nil or nil, err if no row is claimed without error.
			// Let's verify what ClaimNext returns in repository.go
			p.log.Error("failed to claim next outbox event", zap.Error(err))
			return
		}
		if claim == nil {
			return
		}

		p.processEvent(ctx, claim)
	}
}

// finish calls fn to finalize the event. When p.finisher is nil (e.g.
// test pool without a real repo), the call is silently skipped and the
// event remains in 'processing' — the reclaim loop will pick it up.
func (p *Pool) finish(ctx context.Context, evt Event, claim *Claim, fn func() error, label string) {
	if p.finisher == nil {
		p.log.Warn("finisher is nil — event will be reclaimed",
			zap.Int64("event_id", evt.ID),
			zap.String("type", evt.EventType),
			zap.String("label", label))
		return
	}
	if err := fn(); err != nil {
		p.log.Error("failed to finalize event",
			zap.Int64("event_id", evt.ID),
			zap.String("label", label),
			zap.Error(err))
	}
}

func (p *Pool) processEvent(ctx context.Context, claim *Claim) {
	evt := claim.Event
	p.log.Info("processing event", zap.Int64("event_id", evt.ID), zap.String("type", evt.EventType))

	// ── Panic recovery (FASE 2.2, July 2026) ───────────────────────
	// A handler panic must NOT kill the worker. Convert the panic
	// into a terminal error and dead-letter the event immediately.
	// The deferred recover runs OUTSIDE the handler-scoped block so
	// it also catches panics in the Get / missing-handler path.
	var handlerErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				if e, ok := r.(error); ok {
					handlerErr = NewTerminalError(fmt.Errorf("handler panic: %w", e))
				} else {
					handlerErr = NewTerminalError(fmt.Errorf("handler panic: %v", r))
				}
				p.log.Error("handler panicked — dead-lettering event",
					zap.Int64("event_id", evt.ID),
					zap.String("type", evt.EventType),
					zap.Any("panic", r))
			}
		}()

		handler, ok := p.registry.Get(evt.EventType)
		if !ok {
			// FASE 2.2: missing handler → dead_letter immediately.
			// No retry — this event type will never acquire a handler.
			handlerErr = NewTerminalError(fmt.Errorf("no handler registered for event type %s", evt.EventType))
			p.log.Error("missing event handler — dead-lettering immediately",
				zap.Int64("event_id", evt.ID),
				zap.String("type", evt.EventType))
			return
		}

		// Create timeout context for processing
		procCtx, cancel := context.WithTimeout(ctx, p.cfg.ProcessTimeout)
		defer cancel()

		handlerErr = handler.Handle(procCtx, evt)
	}()

	// ── Route by error classification ────────────────────────────
	if handlerErr != nil {
		// QDRANT-002 item F: supersede → terminal success.
		if IsSupersede(handlerErr) {
			p.log.Info("event superseded by newer aggregate version — closing as superseded",
				zap.Int64("event_id", evt.ID),
				zap.String("type", evt.EventType),
				zap.Int("attempt", evt.AttemptCount),
				zap.Error(handlerErr))
			p.finish(ctx, evt, claim, func() error {
				return p.finisher.MarkSuperseded(ctx, evt.ID, claim.LeaseID, handlerErr.Error())
			}, "superseded")
			return
		}

		// QDRANT-002 item G: terminal error → dead_letter.
		// Also catches missing-handler and handler-panic errors
		// now wrapped with NewTerminalError (FASE 2.2).
		if IsTerminal(handlerErr) {
			p.log.Warn("handler returned terminal error — dead-lettering without retry",
				zap.Int64("event_id", evt.ID),
				zap.String("type", evt.EventType),
				zap.Int("attempt", evt.AttemptCount),
				zap.Error(handlerErr))
			p.finish(ctx, evt, claim, func() error {
				return p.finisher.MarkDeadLetter(ctx, evt.ID, claim.LeaseID, handlerErr.Error())
			}, "dead-letter")
			return
		}

		// Retryable error: apply jittered backoff (FASE 2.2).
		p.log.Error("handler failed (retryable) — applying jittered backoff",
			zap.Int64("event_id", evt.ID),
			zap.Int("attempt", evt.AttemptCount),
			zap.Error(handlerErr))
		nextAttempt := p.computeNextAttempt(evt.AttemptCount + 1)
		p.finish(ctx, evt, claim, func() error {
			return p.finisher.MarkFailed(ctx, evt.ID, claim.LeaseID, handlerErr.Error(), nextAttempt)
		}, "failed")
		return
	}

	p.log.Info("event processed successfully", zap.Int64("event_id", evt.ID))
	p.finish(ctx, evt, claim, func() error {
		return p.finisher.MarkCompleted(ctx, evt.ID, claim.LeaseID)
	}, "completed")
}
