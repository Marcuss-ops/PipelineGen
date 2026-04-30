package job

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"velox/go-master/pkg/config"
	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/models"
	"go.uber.org/zap"
)

var (
	ErrJobNotFound      = errors.New("job not found")
	ErrJobAlreadyExists = errors.New("job already exists")
	ErrInvalidJobStatus = errors.New("invalid job status transition")
	ErrQueueFull        = errors.New("job queue is full")
)

type Service struct {
	storage       StorageInterface
	cfg           *config.Config
	mu            sync.RWMutex
	queue         *models.Queue
	newJobsPaused bool
}

func NewService(storage StorageInterface, cfg *config.Config) *Service {
	if cfg == nil {
		cfg = config.Get()
	}
	return &Service{
		storage:       storage,
		cfg:           cfg,
		queue:         &models.Queue{Jobs: []*models.Job{}, UpdatedAt: time.Now()},
		newJobsPaused: cfg.Jobs.NewJobsPaused,
	}
}

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
	defer s.mu.Unlock()

	for _, j := range s.queue.Jobs {
		if j.ID == job.ID {
			return nil, ErrJobAlreadyExists
		}
	}
	s.queue.Jobs = append(s.queue.Jobs, job)

	if err := s.SaveQueue(ctx); err != nil {
		return nil, err
	}

	s.logEvent(ctx, job.ID, "JOB_CREATED", "Job created and queued")

	logger.Info("Job created",
		zap.String("job_id", job.ID),
		zap.String("type", string(job.Type)),
		zap.String("project", job.Project),
	)

	return job, nil
}

func (s *Service) GetJob(ctx context.Context, id string) (*models.Job, error) {
	s.mu.RLock()
	job, _ := s.findJobInMemory(id)
	s.mu.RUnlock()

	if job != nil {
		return job, nil
	}

	return s.storage.GetJob(ctx, id)
}

func (s *Service) ListJobs(ctx context.Context, filter models.JobFilter) ([]*models.Job, error) {
	return s.storage.ListJobs(ctx, filter)
}

func (s *Service) GetAllJobs() []*models.Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	jobs := make([]*models.Job, len(s.queue.Jobs))
	copy(jobs, s.queue.Jobs)
	return jobs
}

func (s *Service) findJobInMemory(jobID string) (*models.Job, int) {
	for i, j := range s.queue.Jobs {
		if j.ID == jobID {
			return j, i
		}
	}
	return nil, -1
}
