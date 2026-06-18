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
	name      string
	repo      *Repository
	registry  *HandlerRegistry
	log       *zap.Logger
	cfg       WorkerPollConfig
	stopChan  chan struct{}
	wg        sync.WaitGroup
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

func (p *Pool) Stop(timeout time.Duration) error {
	p.log.Info("stopping outbox events pool")
	close(p.stopChan)

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
		p.log.Error("handler failed", zap.Int64("event_id", evt.ID), zap.Error(err))
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
