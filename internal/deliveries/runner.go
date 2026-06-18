package deliveries

import (
	"context"
	"syscall"
	"time"

	"go.uber.org/zap"
)

// RunnerConfig holds configuration for the delivery runner.
type RunnerConfig struct {
	WorkerID   string
	PollEvery  time.Duration
	LeaseTTL   time.Duration
	RequeueTTL time.Duration
}

// DefaultRunnerConfig returns sensible defaults.
func DefaultRunnerConfig() RunnerConfig {
	return RunnerConfig{
		WorkerID:   "delivery-runner",
		PollEvery:  5 * time.Second,
		LeaseTTL:   5 * time.Minute,
		RequeueTTL: 30 * time.Second,
	}
}

// Runner is a background worker that polls for PENDING deliveries,
// claims them, and executes them via registered providers.
type Runner struct {
	svc  *Service
	log  *zap.Logger
	cfg  RunnerConfig
	done chan struct{}
}

// NewRunner creates a new delivery runner.
func NewRunner(svc *Service, log *zap.Logger, cfg RunnerConfig) *Runner {
	if log == nil {
		log = zap.NewNop()
	}
	if cfg.WorkerID == "" {
		cfg.WorkerID = "delivery-runner"
	}
	if cfg.PollEvery <= 0 {
		cfg.PollEvery = 5 * time.Second
	}
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = 5 * time.Minute
	}
	if cfg.RequeueTTL <= 0 {
		cfg.RequeueTTL = 30 * time.Second
	}
	return &Runner{
		svc:  svc,
		log:  log,
		cfg:  cfg,
		done: make(chan struct{}),
	}
}

// Start begins the delivery polling loop. Blocks until ctx is cancelled.
func (r *Runner) Start(ctx context.Context) {
	r.log.Info("delivery runner started",
		zap.String("worker_id", r.cfg.WorkerID),
		zap.Duration("poll_every", r.cfg.PollEvery),
		zap.Duration("lease_ttl", r.cfg.LeaseTTL),
		zap.Duration("requeue_ttl", r.cfg.RequeueTTL),
	)

	// Stale lease reclaimer - runs on separate ticker
	go r.requeueLoop(ctx)

	ticker := time.NewTicker(r.cfg.PollEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.log.Info("delivery runner shutting down")
			close(r.done)
			return
		case <-ticker.C:
			r.processOne(ctx)
		}
	}
}

// Shutdown blocks until the runner has fully stopped.
func (r *Runner) Shutdown(timeout time.Duration) error {
	select {
	case <-r.done:
		return nil
	case <-time.After(timeout):
		r.log.Warn("delivery runner shutdown timed out")
		return nil
	}
}

// requeueLoop periodically reclaims stale deliveries.
func (r *Runner) requeueLoop(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.RequeueTTL)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stale, err := r.svc.RequeueStale(ctx, time.Now(), 1000)
			if err != nil {
				r.log.Warn("delivery requeue failed", zap.Error(err))
			} else if len(stale) > 0 {
				r.log.Info("requeued stale deliveries", zap.Int("count", len(stale)))
			}
		}
	}
}

// processOne claims and executes a single delivery with lease renewal.
func (r *Runner) processOne(ctx context.Context) {
	d, err := r.svc.ClaimNext(ctx, r.cfg.WorkerID, r.cfg.LeaseTTL)
	if err != nil {
		r.log.Warn("delivery claim failed", zap.Error(err))
		return
	}
	if d == nil {
		return
	}

	r.log.Info("claimed delivery",
		zap.String("id", d.ID),
		zap.String("artifact_id", d.ArtifactID),
		zap.String("destination_id", d.DestinationID),
		zap.String("provider", d.Provider),
	)

	// Lease renewal goroutine — prevents RequeueStale from reclaiming
	// this delivery while it's executing.
	stopLease := make(chan struct{})
	go r.renewLeaseLoop(ctx, d.ID, d.LockedBy, stopLease)

	err = r.svc.Execute(ctx, d)

	// Stop lease renewal
	close(stopLease)

	if err != nil {
		r.log.Warn("delivery execution failed",
			zap.String("id", d.ID),
			zap.Error(err),
		)
	}
}

// renewLeaseLoop periodically extends the lease for an in-flight delivery.
func (r *Runner) renewLeaseLoop(ctx context.Context, deliveryID, lockedBy string, stop <-chan struct{}) {
	interval := r.cfg.LeaseTTL / 3
	if interval < 10*time.Second {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.svc.repo.RenewLease(ctx, deliveryID, lockedBy, r.cfg.LeaseTTL); err != nil {
				r.log.Warn("failed to renew delivery lease",
					zap.String("id", deliveryID),
					zap.Error(err),
				)
			}
		}
	}
}

// Signal handling for graceful shutdown.
var ShutdownSignals = []syscall.Signal{syscall.SIGINT, syscall.SIGTERM}
