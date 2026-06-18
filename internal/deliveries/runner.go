package deliveries

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// RunnerConfig holds configuration for the delivery runner.
type RunnerConfig struct {
	PollEvery   time.Duration
	LeaseTTL    time.Duration
	RequeueTTL  time.Duration
}

// DefaultRunnerConfig returns sensible defaults.
func DefaultRunnerConfig() RunnerConfig {
	return RunnerConfig{
		PollEvery:  5 * time.Second,
		LeaseTTL:   5 * time.Minute,
		RequeueTTL: 30 * time.Second,
	}
}

// Runner is a background worker that polls for PENDING deliveries,
// claims them, and executes them via registered providers.
type Runner struct {
	svc    *Service
	log    *zap.Logger
	cfg    RunnerConfig
}

// NewRunner creates a new delivery runner.
func NewRunner(svc *Service, log *zap.Logger, cfg RunnerConfig) *Runner {
	if log == nil {
		log = zap.NewNop()
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
	return &Runner{svc: svc, log: log, cfg: cfg}
}

// Start begins the delivery polling loop. Blocks until ctx is cancelled.
func (r *Runner) Start(ctx context.Context) {
	r.log.Info("delivery runner started",
		zap.Duration("poll_every", r.cfg.PollEvery),
		zap.Duration("lease_ttl", r.cfg.LeaseTTL),
	)

	ticker := time.NewTicker(r.cfg.PollEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.log.Info("delivery runner stopped")
			return
		case <-ticker.C:
			r.processBatch(ctx)
		}
	}
}

// processBatch claims and executes available deliveries.
func (r *Runner) processBatch(ctx context.Context) {
	// Count available deliveries
	d, err := r.svc.ClaimNext(ctx, "delivery-runner", r.cfg.LeaseTTL)
	if err != nil {
		r.log.Warn("delivery claim failed", zap.Error(err))
		return
	}
	if d == nil {
		return // nothing to process
	}

	r.log.Info("claimed delivery",
		zap.String("id", d.ID),
		zap.String("artifact_id", d.ArtifactID),
		zap.String("provider", d.Provider),
	)

	// Execute the delivery
	err = r.svc.Execute(ctx, d, func(ctx context.Context) (ProviderRequest, error) {
		return ProviderRequest{
			DeliveryID: d.ID,
			ArtifactID: d.ArtifactID,
		}, nil
	})

	if err != nil {
		r.log.Warn("delivery execution failed",
			zap.String("id", d.ID),
			zap.Error(err),
		)
	}
}
