// Package maintenance provides background maintenance tasks for the VeloxEditing system.
package maintenance

import (
	"context"
	"time"

	"velox/go-master/internal/core/job"
	"velox/go-master/internal/core/worker"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// Service manages background maintenance tasks
type Service struct {
	cfg           *config.Config
	jobService    *job.Service
	workerService *worker.Service
}

// New creates a new maintenance Service
func New(cfg *config.Config, jobService *job.Service, workerService *worker.Service) *Service {
	return &Service{
		cfg:           cfg,
		jobService:    jobService,
		workerService: workerService,
	}
}

// Start launches all background maintenance goroutines.
// It respects context cancellation and cleans up on exit.
func (s *Service) Start(ctx context.Context) {
	go s.zombieChecker(ctx)
	go s.autoCleanup(ctx)
	go s.workerOfflineChecker(ctx)
	go s.autoSave(ctx)
}

func (s *Service) zombieChecker(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(s.cfg.Jobs.ZombieCheckInterval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Zombie checker stopped")
			return
		case <-ticker.C:
			zCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			zombies := s.jobService.CheckAndKillZombieJobs(zCtx)
			if len(zombies) > 0 {
				logger.Info("Zombie jobs detected", zap.Strings("job_ids", zombies))
			}
			cancel()
		}
	}
}

func (s *Service) autoCleanup(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(s.cfg.Jobs.CleanupInterval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Auto cleanup stopped")
			return
		case <-ticker.C:
			aCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			deleted := s.jobService.AutoCleanupJobs(aCtx)
			if deleted > 0 {
				logger.Info("Auto-cleanup completed", zap.Int("deleted", deleted))
			}
			cancel()
		}
	}
}

func (s *Service) workerOfflineChecker(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(s.cfg.Workers.HeartbeatTimeout) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Worker offline checker stopped")
			return
		case <-ticker.C:
			offline := s.workerService.CheckOfflineWorkers()
			if len(offline) > 0 {
				logger.Info("Workers marked offline", zap.Strings("worker_ids", offline))

				// Save workers state
				wCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				if err := s.workerService.SaveWorkers(wCtx); err != nil {
					logger.Error("Failed to save workers", zap.Error(err))
				}
				cancel()
			}
		}
	}
}

func (s *Service) autoSave(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(s.cfg.Storage.AutoSaveInterval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Auto save stopped")
			return
		case <-ticker.C:
			aCtx, cancel := context.WithTimeout(ctx, 30*time.Second)

			if err := s.jobService.SaveQueue(aCtx); err != nil {
				logger.Error("Failed to auto-save queue", zap.Error(err))
			}

			if err := s.workerService.SaveWorkers(aCtx); err != nil {
				logger.Error("Failed to auto-save workers", zap.Error(err))
			}

			cancel()
		}
	}
}
