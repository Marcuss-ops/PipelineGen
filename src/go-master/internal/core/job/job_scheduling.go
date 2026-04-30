package job

import (
	"context"
	"time"

	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/models"
	"go.uber.org/zap"
)

func (s *Service) GetNextPendingJob(workerCapabilities []models.WorkerCapability, workerID string) *models.Job {
	s.mu.RLock()
	defer s.mu.RUnlock()

	runningPerProject := make(map[string]int)
	for _, job := range s.queue.Jobs {
		if job.Status == models.StatusRunning {
			runningPerProject[job.Project]++
		}
	}

	for _, job := range s.queue.Jobs {
		if job.Status != models.StatusPending && job.Status != models.StatusQueued {
			continue
		}

		if runningPerProject[job.Project] >= s.cfg.Jobs.MaxParallelPerProject {
			continue
		}

		if !s.workerCanHandleJob(workerCapabilities, job.Type) {
			continue
		}

		return job
	}

	return nil
}

func (s *Service) CheckAndKillZombieJobs(ctx context.Context) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	var zombies []string

	for _, job := range s.queue.Jobs {
		if job.Status != models.StatusRunning {
			continue
		}

		if job.LeaseExpiry != nil && job.LeaseExpiry.Before(now) {
			job.Status = models.StatusZombie
			job.Error = "Job lease expired - worker likely died"
			job.UpdatedAt = now
			zombies = append(zombies, job.ID)

			s.logEvent(ctx, job.ID, "JOB_ZOMBIE", "Job marked as zombie - lease expired")
			logger.Warn("Zombie job detected",
				zap.String("job_id", job.ID),
				zap.String("worker_id", job.WorkerID),
			)
		}
	}

	if len(zombies) > 0 {
		s.SaveQueue(ctx)
	}

	return zombies
}

func (s *Service) AutoCleanupJobs(ctx context.Context) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-time.Duration(s.cfg.Jobs.AutoCleanupHours) * time.Hour)
	var toDelete []int

	for i, job := range s.queue.Jobs {
		if job.Status != models.StatusCompleted && job.Status != models.StatusFailed && job.Status != models.StatusCancelled {
			continue
		}

		if job.CompletedAt != nil && job.CompletedAt.Before(cutoff) {
			toDelete = append(toDelete, i)
		}
	}

	for i := len(toDelete) - 1; i >= 0; i-- {
		idx := toDelete[i]
		job := s.queue.Jobs[idx]
		s.queue.Jobs = append(s.queue.Jobs[:idx], s.queue.Jobs[idx+1:]...)
		s.logEvent(ctx, job.ID, "JOB_CLEANUP", "Old job auto-cleaned")
	}

	if len(toDelete) > 0 {
		s.SaveQueue(ctx)
		logger.Info("Auto-cleanup completed", zap.Int("deleted_count", len(toDelete)))
	}

	return len(toDelete)
}

func (s *Service) IsNewJobsPaused() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.newJobsPaused
}

func (s *Service) SetNewJobsPaused(paused bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.newJobsPaused = paused
	logger.Info("New jobs pause state changed", zap.Bool("paused", paused))
}
