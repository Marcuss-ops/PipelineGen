package worker

import (
	"errors"
	"sync"

	"velox/go-master/pkg/config"
	"velox/go-master/pkg/models"
)

var (
	ErrWorkerNotFound      = errors.New("worker not found")
	ErrWorkerAlreadyExists = errors.New("worker already exists")
	ErrInvalidToken        = errors.New("invalid worker token")
	ErrWorkerRevoked       = errors.New("worker has been revoked")
	ErrWorkerQuarantined   = errors.New("worker is quarantined")
)

// Service provides worker management business logic
type Service struct {
	storage            StorageInterface
	cfg                *config.Config
	mu                 sync.RWMutex
	workers            map[string]*models.Worker
	workerTokens       map[string]string // token -> workerID
	revokedWorkers     map[string]bool
	quarantinedWorkers map[string]*QuarantineInfo
	failHistory        []FailHistoryEntry
}

// NewService creates a new worker service
func NewService(storage StorageInterface, cfg *config.Config) *Service {
	if cfg == nil {
		cfg = config.Get()
	}
	return &Service{
		storage:            storage,
		cfg:                cfg,
		workers:            make(map[string]*models.Worker),
		workerTokens:       make(map[string]string),
		revokedWorkers:     make(map[string]bool),
		quarantinedWorkers: make(map[string]*QuarantineInfo),
		failHistory:        make([]FailHistoryEntry, 0),
	}
}

// GetWorker retrieves a worker by ID (returns a clone)
func (s *Service) GetWorker(id string) (*models.Worker, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	worker, exists := s.workers[id]
	if !exists {
		return nil, ErrWorkerNotFound
	}
	return worker.Clone(), nil
}

// GetWorkerByToken retrieves a worker by its token
func (s *Service) GetWorkerByToken(token string) (*models.Worker, error) {
	s.mu.RLock()
	workerID, exists := s.workerTokens[token]
	s.mu.RUnlock()

	if !exists {
		return nil, ErrInvalidToken
	}

	return s.GetWorker(workerID)
}

// ListWorkers returns all workers (clones)
func (s *Service) ListWorkers() []*models.Worker {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workers := make([]*models.Worker, 0, len(s.workers))
	for _, worker := range s.workers {
		workers = append(workers, worker.Clone())
	}
	return workers
}

// ListActiveWorkers returns all active (online) workers (clones)
func (s *Service) ListActiveWorkers() []*models.Worker {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workers := make([]*models.Worker, 0)
	for _, worker := range s.workers {
		if worker.Status == models.WorkerStatusOnline || worker.Status == models.WorkerStatusBusy {
			workers = append(workers, worker.Clone())
		}
	}
	return workers
}

// IsWorkerRevoked checks if a worker is revoked
func (s *Service) IsWorkerRevoked(workerID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revokedWorkers[workerID]
}

// IsWorkerQuarantined checks if a worker is quarantined
func (s *Service) IsWorkerQuarantined(workerID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.quarantinedWorkers[workerID]
	return exists
}

// IsWorkerSchedulable checks if a worker can be scheduled for jobs
func (s *Service) IsWorkerSchedulable(worker *models.Worker) bool {
	if worker.Status != models.WorkerStatusOnline {
		return false
	}
	if s.IsWorkerRevoked(worker.ID) {
		return false
	}
	if s.IsWorkerQuarantined(worker.ID) {
		return false
	}
	if worker.MaintenanceMode {
		return false
	}
	return true
}
