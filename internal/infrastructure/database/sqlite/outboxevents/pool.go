package outboxevents

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

type WorkerPollConfig struct {
	PollInterval    time.Duration
	ProcessTimeout  time.Duration
	ReclaimInterval time.Duration
}

type Pool struct {
	name     string
	repo     *Repository
	registry *HandlerRegistry
	log      *zap.Logger
	cfg      WorkerPollConfig
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
		registry: registry,
		log:      log.With(zap.String("pool", name)),
		cfg:      cfg,
		stopChan: make(chan struct{}),
	}
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

func (p *Pool) processEvent(ctx context.Context, claim *Claim) {
	evt := claim.Event
	p.log.Info("processing event", zap.Int64("event_id", evt.ID), zap.String("type", evt.EventType))

	handler, ok := p.registry.Get(evt.EventType)
	if !ok {
		err := fmt.Errorf("no handler registered for event type %s", evt.EventType)
		p.log.Error("missing event handler", zap.Int64("event_id", evt.ID), zap.Error(err))
		nextAttempt := time.Now().Add(5 * time.Second)
		if markErr := p.repo.MarkFailed(ctx, evt.ID, claim.LeaseID, err.Error(), nextAttempt); markErr != nil {
			p.log.Error("failed to mark event as failed", zap.Int64("event_id", evt.ID), zap.Error(markErr))
		}
		return
	}

	// Create timeout context for processing
	procCtx, cancel := context.WithTimeout(ctx, p.cfg.ProcessTimeout)
	defer cancel()

	err := handler.Handle(procCtx, evt)
	if err != nil {
		// QDRANT-002 item F: classify a *SupersedeError as
		// success-like. The handler determined the event was
		// obsoleted by a newer aggregate version, so re-running it
		// would burn a wasted upsert for no gain. Route to
		// status='superseded' (terminal, distinct from dead_letter
		// so dashboards can tell "producer broken" apart from
		// "upstream streamed a fresh update — old events no-op").
		//
		// Order matters: supersede is checked BEFORE IsTerminal. The
		// two are not mutually exclusive (a future handler could
		// wrap either around the other) but in practice a handler
		// returns exactly one shape per error path, so first match
		// wins.
		if IsSupersede(err) {
			p.log.Info("event superseded by newer aggregate version — closing as superseded",
				zap.Int64("event_id", evt.ID),
				zap.String("type", evt.EventType),
				zap.Int("attempt", evt.AttemptCount),
				zap.Error(err))
			if markErr := p.repo.MarkSuperseded(ctx, evt.ID, claim.LeaseID, err.Error()); markErr != nil {
				// Lease-lost is the only acceptable non-success here
				// (another worker raced us to terminal status).
				// Surface all other failures loudly.
				p.log.Error("failed to supersede event",
					zap.Int64("event_id", evt.ID),
					zap.Error(markErr))
			}
			return
		}
		// QDRANT-002 item G: classify errors as retryable vs terminal.
		// Terminal errors (malformed payload, schema mismatch, unknown
		// provider, unsupported destination) bypass the exponential
		// backoff and go straight to dead_letter. Retryable errors
		// keep the existing backoff path; after max_attempts the row
		// dead_letters naturally via MarkFailed.
		//
		// IsTerminal recognises both the typed *TerminalError wrap
		// (canonical path) and the legacy "(terminal)" string
		// breadcrumb already inlined by delivery.go / provider_sync.go
		// — see outboxevents/errors.go for the rationale.
		if IsTerminal(err) {
			p.log.Warn("handler returned terminal error — dead-lettering without retry",
				zap.Int64("event_id", evt.ID),
				zap.String("type", evt.EventType),
				zap.Int("attempt", evt.AttemptCount),
				zap.Error(err))
			if markErr := p.repo.MarkDeadLetter(ctx, evt.ID, claim.LeaseID, err.Error()); markErr != nil {
				p.log.Error("failed to dead-letter terminal event",
					zap.Int64("event_id", evt.ID),
					zap.Error(markErr))
			}
			return
		}
		p.log.Error("handler failed (retryable) — applying backoff",
			zap.Int64("event_id", evt.ID),
			zap.Int("attempt", evt.AttemptCount),
			zap.Error(err))
		nextAttempt := time.Now().Add(time.Duration(1<<evt.AttemptCount) * time.Minute)
		if markErr := p.repo.MarkFailed(ctx, evt.ID, claim.LeaseID, err.Error(), nextAttempt); markErr != nil {
			p.log.Error("failed to mark event as failed", zap.Int64("event_id", evt.ID), zap.Error(markErr))
		}
		return
	}

	p.log.Info("event processed successfully", zap.Int64("event_id", evt.ID))
	if markErr := p.repo.MarkCompleted(ctx, evt.ID, claim.LeaseID); markErr != nil {
		p.log.Error("failed to mark event as completed", zap.Int64("event_id", evt.ID), zap.Error(markErr))
	}
}
