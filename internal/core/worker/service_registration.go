package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/models"
)

// LoadWorkers loads workers from storage
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

// SaveWorkers saves workers to storage
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

// RegisterWorker registers a new worker
func (s *Service) RegisterWorker(ctx context.Context, req models.WorkerRegistrationRequest) (*models.Worker, string, error) {
	// Validate IP if allowed IPs are configured
	if len(s.cfg.Workers.AllowedIPs) > 0 {
		if !s.isIPAllowed(req.IP) {
			return nil, "", fmt.Errorf("worker IP %s is not in allowed list", req.IP)
		}
	}

	// Generate unique ID and token
	workerID := uuid.New().String()
	token := s.generateToken()

	now := time.Now()
	worker := &models.Worker{
		ID:                workerID,
		Name:              req.Name,
		Hostname:          req.Hostname,
		IP:                req.IP,
		Port:              req.Port,
		Status:            models.WorkerStatusOnline,
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

	// Return a clone to avoid exposing internal pointer
	return worker.Clone(), token, nil
}
