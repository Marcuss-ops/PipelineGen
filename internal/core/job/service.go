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

// Service provides job management business logic
type Service struct {
	storage       StorageInterface
	cfg           *config.Config
	mu            sync.RWMutex
	queue         *models.Queue
	newJobsPaused bool
}

// NewService creates a new job service
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

// UpdateJobStatus updates the status of a job
func (s *Service) UpdateJobStatus(ctx context.Context, jobID string, status models.JobStatus, progress int, result map[string]interface{}, errorMsg string) error {
	// Find job in memory first (under lock)
	s.mu.Lock()
	var job *models.Job
	for _, j := range s.queue.Jobs {
		if j.ID == jobID {
			job = j
			break
		}
	}
	s.mu.Unlock()

	// Fall back to storage if not in memory
	if job == nil {
		var err error
		job, err = s.storage.GetJob(ctx, jobID)
		if err != nil {
			return err
		}
	}

	// Clone job to avoid mutating the shared pointer
	jobClone := job.Clone()

	// Validate status transition
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

	// Update the in-memory job under lock
	s.mu.Lock()
	for i, j := range s.queue.Jobs {
		if j.ID == jobID {
			s.queue.Jobs[i] = jobClone
			break
		}
	}
	s.mu.Unlock()

	// Save to storage
	if err := s.storage.SaveJob(ctx, jobClone); err != nil {
		logger.Error("Failed to save job", zap.Error(err))
		// Also save the full queue to keep consistency
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

// AssignJobToWorker assigns a job to a worker
func (s *Service) AssignJobToWorker(ctx context.Context, jobID string, workerID string) error {
	s.mu.Lock()
	var job *models.Job
	var idx = -1
	for i, j := range s.queue.Jobs {
		if j.ID == jobID {
			job = j
			idx = i
			break
		}
	}
	s.mu.Unlock()

	if job == nil {
		return ErrJobNotFound
	}

	if job.Status != models.StatusPending && job.Status != models.StatusQueued {
		return fmt.Errorf("job is not in assignable state: %s", job.Status)
	}

	now := time.Now()
	leaseExpiry := now.Add(time.Duration(s.cfg.Jobs.LeaseTTLSeconds) * time.Second)

	// Clone and modify
	jobClone := job.Clone()
	jobClone.Status = models.StatusRunning
	jobClone.WorkerID = workerID
	jobClone.StartedAt = &now
	jobClone.LeaseExpiry = &leaseExpiry
	jobClone.UpdatedAt = now

	// Update in memory under lock
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

// RenewJobLease renews the lease for a running job
func (s *Service) RenewJobLease(ctx context.Context, jobID string) error {
	s.mu.Lock()
	var job *models.Job
	var idx = -1
	for i, j := range s.queue.Jobs {
		if j.ID == jobID {
			job = j
			idx = i
			break
		}
	}
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

// GetNextPendingJob returns the next pending job for a worker
func (s *Service) GetNextPendingJob(workerCapabilities []models.WorkerCapability, workerID string) *models.Job {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Count running jobs per project
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

		// Check project parallelism limit
		if runningPerProject[job.Project] >= s.cfg.Jobs.MaxParallelPerProject {
			continue
		}

		// Check if worker has required capabilities
		if !s.workerCanHandleJob(workerCapabilities, job.Type) {
			continue
		}

		return job
	}

	return nil
}

// CheckAndKillZombieJobs checks for zombie jobs (running but lease expired)
func (s *Service) CheckAndKillZombieJobs(ctx context.Context) []string {
	s.mu.Lock()
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
	s.mu.Unlock()

	if len(zombies) > 0 {
		s.SaveQueue(ctx)
	}

	return zombies
}

// AutoCleanupJobs removes old completed/failed jobs
func (s *Service) AutoCleanupJobs(ctx context.Context) int {
	s.mu.Lock()
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

	// Delete in reverse order to maintain indices
	for i := len(toDelete) - 1; i >= 0; i-- {
		idx := toDelete[i]
		job := s.queue.Jobs[idx]
		s.queue.Jobs = append(s.queue.Jobs[:idx], s.queue.Jobs[idx+1:]...)
		s.logEvent(ctx, job.ID, "JOB_CLEANUP", "Old job auto-cleaned")
	}
	s.mu.Unlock()

	if len(toDelete) > 0 {
		s.SaveQueue(ctx)
		logger.Info("Auto-cleanup completed", zap.Int("deleted_count", len(toDelete)))
	}

	return len(toDelete)
}

// IsNewJobsPaused returns whether new job creation is paused
func (s *Service) IsNewJobsPaused() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.newJobsPaused
}

// SetNewJobsPaused sets whether new job creation is paused
func (s *Service) SetNewJobsPaused(paused bool) {
	s.mu.Lock()
	s.newJobsPaused = paused
	s.mu.Unlock()
	logger.Info("New jobs pause state changed", zap.Bool("paused", paused))
}

// workerCanHandleJob checks if a worker can handle a job type
func (s *Service) workerCanHandleJob(capabilities []models.WorkerCapability, jobType models.JobType) bool {
	required := s.getRequiredCapability(jobType)
	if required == "" {
		return true
	}

	for _, cap := range capabilities {
		if cap == required {
			return true
		}
	}
	return false
}

// getRequiredCapability returns the required capability for a job type
func (s *Service) getRequiredCapability(jobType models.JobType) models.WorkerCapability {
	switch jobType {
	case models.TypeVideoGeneration:
		return models.CapVideoGeneration
	case models.TypeVoiceover:
		return models.CapTTS
	case models.TypeScriptGen:
		return models.CapVideoGeneration
	case models.TypeStockDownload:
		return models.CapStockDownload
	case models.TypeUpload:
		return models.CapUpload
	default:
		return ""
	}
}

// isValidStatusTransition checks if a status transition is valid
func (s *Service) isValidStatusTransition(from, to models.JobStatus) bool {
	// Define valid transitions
	validTransitions := map[models.JobStatus][]models.JobStatus{
		models.StatusPending:   {models.StatusQueued, models.StatusCancelled},
		models.StatusQueued:    {models.StatusRunning, models.StatusCancelled},
		models.StatusRunning:   {models.StatusCompleted, models.StatusFailed, models.StatusZombie, models.StatusCancelled},
		models.StatusZombie:    {models.StatusQueued, models.StatusFailed, models.StatusCancelled},
		models.StatusFailed:    {models.StatusQueued, models.StatusCancelled},
		models.StatusCompleted: {},
		models.StatusCancelled: {},
		models.StatusRetrying:  {models.StatusQueued, models.StatusCancelled},
	}

	allowed, exists := validTransitions[from]
	if !exists {
		return false
	}

	for _, status := range allowed {
		if status == to {
			return true
		}
	}
	return false
}

// logEvent logs a job event
func (s *Service) logEvent(ctx context.Context, jobID, eventType, message string) {
	event := &models.JobEvent{
		ID:        fmt.Sprintf("%s-%d", jobID, time.Now().UnixNano()),
		JobID:     jobID,
		Type:      eventType,
		Message:   message,
		Timestamp: time.Now(),
	}
	s.storage.LogJobEvent(ctx, event)
}