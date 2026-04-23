package job

import (
	"context"
	"errors"
	"fmt"
	"time"

	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/models"
	"go.uber.org/zap"
)

// LoadQueue loads the job queue from storage
func (s *Service) LoadQueue(ctx context.Context) error {
	queue, err := s.storage.LoadQueue(ctx)
	if err != nil {
		logger.Error("Failed to load queue", zap.Error(err))
		return fmt.Errorf("failed to load queue: %w", err)
	}
	s.mu.Lock()
	s.queue = queue
	s.mu.Unlock()
	logger.Info("Queue loaded", zap.Int("job_count", len(queue.Jobs)))
	return nil
}

// SaveQueue saves the job queue to storage
func (s *Service) SaveQueue(ctx context.Context) error {
	s.mu.RLock()
	queue := s.queue
	s.mu.RUnlock()

	queue.UpdatedAt = time.Now()
	if err := s.storage.SaveQueue(ctx, queue); err != nil {
		logger.Error("Failed to save queue", zap.Error(err))
		return fmt.Errorf("failed to save queue: %w", err)
	}
	return nil
}

// CreateJob creates a new job and adds it to the queue
func (s *Service) CreateJob(ctx context.Context, req models.CreateJobRequest) (*models.Job, error) {
	if s.IsNewJobsPaused() {
		return nil, errors.New("new jobs are currently paused")
	}

	job := models.NewJobWithProject(req.Type, req.Project, req.VideoName, req.Payload)
	if req.MaxRetries > 0 {
		job.MaxRetries = req.MaxRetries
	}
	if req.Priority > 0 {
		job.Priority = req.Priority
	}

	s.mu.Lock()
	// Check for duplicate ID (shouldn't happen with UUID, but just in case)
	for _, j := range s.queue.Jobs {
		if j.ID == job.ID {
			s.mu.Unlock()
			return nil, ErrJobAlreadyExists
		}
	}
	s.queue.Jobs = append(s.queue.Jobs, job)
	s.mu.Unlock()

	// Save to storage
	if err := s.SaveQueue(ctx); err != nil {
		return nil, err
	}

	// Log event
	s.logEvent(ctx, job.ID, "JOB_CREATED", "Job created and queued")

	logger.Info("Job created",
		zap.String("job_id", job.ID),
		zap.String("type", string(job.Type)),
		zap.String("project", job.Project),
	)

	return job, nil
}

// GetJob retrieves a job by ID
func (s *Service) GetJob(ctx context.Context, id string) (*models.Job, error) {
	// Try memory first
	s.mu.RLock()
	for _, job := range s.queue.Jobs {
		if job.ID == id {
			s.mu.RUnlock()
			return job, nil
		}
	}
	s.mu.RUnlock()

	// Fall back to storage
	return s.storage.GetJob(ctx, id)
}

// ListJobs lists jobs with optional filtering
func (s *Service) ListJobs(ctx context.Context, filter models.JobFilter) ([]*models.Job, error) {
	// Use storage for filtered queries
	return s.storage.ListJobs(ctx, filter)
}

// GetAllJobs returns all jobs in the queue
func (s *Service) GetAllJobs() []*models.Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	jobs := make([]*models.Job, len(s.queue.Jobs))
	copy(jobs, s.queue.Jobs)
	return jobs
}

// DeleteJob deletes a job
func (s *Service) DeleteJob(ctx context.Context, jobID string) error {
	s.mu.Lock()
	found := false
	for i, job := range s.queue.Jobs {
		if job.ID == jobID {
			s.queue.Jobs = append(s.queue.Jobs[:i], s.queue.Jobs[i+1:]...)
			found = true
			break
		}
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
