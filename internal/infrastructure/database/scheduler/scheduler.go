package scheduler

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"go.uber.org/zap"
)

// DriveSyncScheduler is a minimal compatibility shim that preserves the
// existing bootstrap shape while the real scheduler logic migrates.
type DriveSyncScheduler struct {
	log *zap.Logger
}

func NewDriveSyncScheduler(_ any, _ any, _ any, log *zap.Logger, _ any) *DriveSyncScheduler {
	return &DriveSyncScheduler{log: log}
}

func (s *DriveSyncScheduler) Start(ctx context.Context) {
	<-ctx.Done()
}

func (s *DriveSyncScheduler) Stop() {}

// LifecycleScheduler handles recurring lifecycle events and cleanup tasks.
type LifecycleScheduler struct {
	cfg         *config.Config
	jobsService *job.Service
	log         *zap.Logger
}

func NewLifecycleScheduler(cfg *config.Config, jobsService *job.Service, log *zap.Logger) *LifecycleScheduler {
	return &LifecycleScheduler{
		cfg:         cfg,
		jobsService: jobsService,
		log:         log,
	}
}

func (s *LifecycleScheduler) Start(ctx context.Context) {
	s.log.Info("starting lifecycle scheduler")
	<-ctx.Done()
}
