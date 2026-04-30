package job

import (
	"context"
	"fmt"
	"time"

	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/models"
	"go.uber.org/zap"
)

func (s *Service) UpdateJobStatus(ctx context.Context, jobID string, status models.JobStatus, progress int, result map[string]interface{}, errorMsg string) error {
	s.mu.Lock()
	job, _ := s.findJobInMemory(jobID)
	s.mu.Unlock()

	if job == nil {
		var err error
		job, err = s.storage.GetJob(ctx, jobID)
		if err != nil {
			return err
		}
	}

	jobClone := job.Clone()

	if !s.isValidStatusTransition(jobClone.Status, status) {
		logger.Warn("Invalid status transition",
			zap.String("job_id", jobID),
			zap.String("from", string(jobClone.Status)),
			zap.String("to", string(status)),
		)
		return ErrInvalidJobStatus
	}

	now := time.Now()
	jobClone.Status = status
	jobClone.Progress = progress
	jobClone.UpdatedAt = now

	if result != nil {
		jobClone.Result = result
	}
	if errorMsg != "" {
		jobClone.Error = errorMsg
	}

	switch status {
	case models.StatusRunning:
		jobClone.StartedAt = &now
		s.logEvent(ctx, jobID, "JOB_STARTED", "Job started execution")
	case models.StatusCompleted:
		jobClone.CompletedAt = &now
		s.logEvent(ctx, jobID, "JOB_COMPLETED", "Job completed successfully")
	case models.StatusFailed:
		jobClone.CompletedAt = &now
		jobClone.RetryCount++
		s.logEvent(ctx, jobID, "JOB_FAILED", fmt.Sprintf("Job failed: %s", errorMsg))
	case models.StatusCancelled:
		jobClone.CompletedAt = &now
		s.logEvent(ctx, jobID, "JOB_CANCELLED", "Job cancelled")
	}

	s.mu.Lock()
	for i, j := range s.queue.Jobs {
		if j.ID == jobID {
			s.queue.Jobs[i] = jobClone
			break
		}
	}
	s.mu.Unlock()

	if err := s.storage.SaveJob(ctx, jobClone); err != nil {
		logger.Error("Failed to save job", zap.Error(err))
		if queueErr := s.SaveQueue(ctx); queueErr != nil {
			logger.Error("Failed to save queue as fallback", zap.Error(queueErr))
		}
	}

	logger.Info("Job status updated",
		zap.String("job_id", jobID),
		zap.String("status", string(status)),
		zap.Int("progress", progress),
	)

	return nil
}

func (s *Service) AssignJobToWorker(ctx context.Context, jobID string, workerID string) error {
	s.mu.Lock()
	job, idx := s.findJobInMemory(jobID)
	s.mu.Unlock()

	if job == nil {
		return ErrJobNotFound
	}

	if job.Status != models.StatusPending && job.Status != models.StatusQueued {
		return fmt.Errorf("job is not in assignable state: %s", job.Status)
	}

	now := time.Now()
	leaseExpiry := now.Add(time.Duration(s.cfg.Jobs.LeaseTTLSeconds) * time.Second)

	jobClone := job.Clone()
	jobClone.Status = models.StatusRunning
	jobClone.WorkerID = workerID
	jobClone.StartedAt = &now
	jobClone.LeaseExpiry = &leaseExpiry
	jobClone.UpdatedAt = now

	s.mu.Lock()
	if idx >= 0 && idx < len(s.queue.Jobs) && s.queue.Jobs[idx].ID == jobID {
		s.queue.Jobs[idx] = jobClone
	}
	s.mu.Unlock()

	if err := s.SaveQueue(ctx); err != nil {
		return err
	}

	s.logEvent(ctx, jobID, "JOB_ASSIGNED", fmt.Sprintf("Job assigned to worker %s", workerID))

	logger.Info("Job assigned to worker",
		zap.String("job_id", jobID),
		zap.String("worker_id", workerID),
	)

	return nil
}

func (s *Service) RenewJobLease(ctx context.Context, jobID string) error {
	s.mu.Lock()
	job, idx := s.findJobInMemory(jobID)
	s.mu.Unlock()

	if job == nil {
		return ErrJobNotFound
	}

	if job.Status != models.StatusRunning {
		return fmt.Errorf("job is not running: %s", job.Status)
	}

	leaseExpiry := time.Now().Add(time.Duration(s.cfg.Jobs.LeaseTTLSeconds) * time.Second)
	jobClone := job.Clone()
	jobClone.LeaseExpiry = &leaseExpiry
	jobClone.UpdatedAt = time.Now()

	s.mu.Lock()
	if idx >= 0 && idx < len(s.queue.Jobs) && s.queue.Jobs[idx].ID == jobID {
		s.queue.Jobs[idx] = jobClone
	}
	s.mu.Unlock()

	return s.SaveQueue(ctx)
}

func (s *Service) DeleteJob(ctx context.Context, jobID string) error {
	s.mu.Lock()
	_, idx := s.findJobInMemory(jobID)
	found := idx >= 0
	if found {
		s.queue.Jobs = append(s.queue.Jobs[:idx], s.queue.Jobs[idx+1:]...)
	}
	s.mu.Unlock()

	if !found {
		return ErrJobNotFound
	}

	if err := s.SaveQueue(ctx); err != nil {
		return err
	}

	if err := s.storage.DeleteJob(ctx, jobID); err != nil {
		logger.Warn("Failed to delete job from storage", zap.String("job_id", jobID), zap.Error(err))
	}

	s.logEvent(ctx, jobID, "JOB_DELETED", "Job deleted")

	logger.Info("Job deleted", zap.String("job_id", jobID))
	return nil
}
