package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/models"
	"go.uber.org/zap"
)

var (
	ErrWorkerNotFound      = errors.New("worker not found")
	ErrWorkerAlreadyExists = errors.New("worker already exists")
	ErrInvalidToken        = errors.New("invalid worker token")
	ErrWorkerRevoked       = errors.New("worker has been revoked")
	ErrWorkerQuarantined   = errors.New("worker is quarantined")
)

type Service struct {
	storage           StorageInterface
	cfg               *config.Config
	mu                sync.RWMutex
	workers           map[string]*models.Worker
	workerTokens      map[string]string // token -> workerID
	revokedWorkers    map[string]bool
	quarantinedWorkers map[string]*QuarantineInfo
	failHistory       []FailHistoryEntry
}

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

func (s *Service) LoadWorkers(ctx context.Context) error {
	workers, err := s.storage.LoadWorkers(ctx)
	if err != nil {
		logger.Error("Failed to load workers", zap.Error(err))
		return fmt.Errorf("failed to load workers: %w", err)
	}

	revoked, err := s.storage.LoadRevokedWorkers(ctx)
	if err != nil {
		logger.Warn("Failed to load revoked workers list", zap.Error(err))
		revoked = make(map[string]bool)
	}

	quarantined, err := s.storage.LoadQuarantinedWorkers(ctx)
	if err != nil {
		logger.Warn("Failed to load quarantined workers list", zap.Error(err))
		quarantined = make(map[string]*QuarantineInfo)
	}

	s.mu.Lock()
	s.workers = workers
	s.revokedWorkers = revoked
	s.quarantinedWorkers = quarantined
	
	// Rebuild token map
	s.workerTokens = make(map[string]string)
	for id, worker := range workers {
		if worker.Token != "" {
			s.workerTokens[worker.Token] = id
		}
	}
	s.mu.Unlock()

	logger.Info("Workers loaded", 
		zap.Int("worker_count", len(workers)),
		zap.Int("revoked_count", len(revoked)),
		zap.Int("quarantined_count", len(quarantined)),
	)
	return nil
}

func (s *Service) SaveWorkers(ctx context.Context) error {
	s.mu.RLock()
	workers := make(map[string]*models.Worker, len(s.workers))
	for k, v := range s.workers {
		workers[k] = v
	}
	s.mu.RUnlock()

	if err := s.storage.SaveWorkers(ctx, workers); err != nil {
		logger.Error("Failed to save workers", zap.Error(err))
		return fmt.Errorf("failed to save workers: %w", err)
	}
	return nil
}

func (s *Service) RegisterWorker(ctx context.Context, req models.WorkerRegistrationRequest) (*models.Worker, string, error) {
	if len(s.cfg.Workers.AllowedIPs) > 0 {
		if !s.isIPAllowed(req.IP) {
			return nil, "", fmt.Errorf("worker IP %s is not in allowed list", req.IP)
		}
	}

	workerID := uuid.New().String()
	token := s.generateToken()

	now := time.Now()
	worker := &models.Worker{
		ID:                workerID,
		Name:              req.Name,
		Hostname:          req.Hostname,
		IP:                req.IP,
		Port:              req.Port,
		Status:            models.WorkerIdle,
		Capabilities:      req.Capabilities,
		Version:           req.Version,
		CodeHash:          req.CodeHash,
		Token:             token,
		DiskTotalGB:       req.DiskTotalGB,
		MemoryTotalMB:     req.MemoryTotalMB,
		RegisteredAt:      now,
		LastHeartbeat:     now,
		MaxConcurrentJobs: 2,
		Priority:          5,
		AutoUpdateEnabled: true,
		Tags:              []string{},
		Metadata:          make(map[string]string),
	}

	s.mu.Lock()
	s.workers[workerID] = worker
	s.workerTokens[token] = workerID
	s.mu.Unlock()

	if err := s.SaveWorkers(ctx); err != nil {
		return nil, "", err
	}

	logger.Info("Worker registered",
		zap.String("worker_id", workerID),
		zap.String("name", req.Name),
		zap.String("ip", req.IP),
	)

	return worker.Clone(), token, nil
}

func (s *Service) GetWorker(id string) (*models.Worker, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	worker, exists := s.workers[id]
	if !exists {
		return nil, ErrWorkerNotFound
	}
	return worker.Clone(), nil
}

func (s *Service) GetWorkerByToken(token string) (*models.Worker, error) {
	s.mu.RLock()
	workerID, exists := s.workerTokens[token]
	s.mu.RUnlock()

	if !exists {
		return nil, ErrInvalidToken
	}

	return s.GetWorker(workerID)
}

func (s *Service) ListWorkers() []*models.Worker {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workers := make([]*models.Worker, 0, len(s.workers))
	for _, worker := range s.workers {
		workers = append(workers, worker.Clone())
	}
	return workers
}

func (s *Service) ListActiveWorkers() []*models.Worker {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workers := make([]*models.Worker, 0)
	for _, worker := range s.workers {
		if worker.Status == models.WorkerIdle || worker.Status == models.WorkerBusy {
			workers = append(workers, worker.Clone())
		}
	}
	return workers
}
