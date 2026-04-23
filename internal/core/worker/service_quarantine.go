package worker

import (
	"context"
	"time"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
)

// RevokeWorker revokes a worker
func (s *Service) RevokeWorker(ctx context.Context, workerID string, reason string) error {
	s.mu.Lock()
	delete(s.workers, workerID)
	s.revokedWorkers[workerID] = true
	s.mu.Unlock()

	if err := s.SaveWorkers(ctx); err != nil {
		return err
	}

	if err := s.storage.SaveRevokedWorkers(ctx, s.revokedWorkers); err != nil {
		logger.Warn("Failed to save revoked workers list", zap.Error(err))
	}

	logger.Info("Worker revoked",
		zap.String("worker_id", workerID),
		zap.String("reason", reason),
	)

	return nil
}

// QuarantineWorker quarantines a worker
func (s *Service) QuarantineWorker(ctx context.Context, workerID string, reason string) error {
	s.mu.Lock()
	s.quarantinedWorkers[workerID] = &QuarantineInfo{
		WorkerID:      workerID,
		Reason:        reason,
		QuarantinedAt: time.Now().Unix(),
		ErrorCount:    1,
	}
	s.mu.Unlock()

	if err := s.storage.SaveQuarantinedWorkers(ctx, s.quarantinedWorkers); err != nil {
		logger.Warn("Failed to save quarantined workers list", zap.Error(err))
	}

	logger.Info("Worker quarantined",
		zap.String("worker_id", workerID),
		zap.String("reason", reason),
	)

	return nil
}

// UnquarantineWorker removes a worker from quarantine
func (s *Service) UnquarantineWorker(ctx context.Context, workerID string) error {
	s.mu.Lock()
	delete(s.quarantinedWorkers, workerID)
	s.mu.Unlock()

	if err := s.storage.SaveQuarantinedWorkers(ctx, s.quarantinedWorkers); err != nil {
		return err
	}

	logger.Info("Worker unquarantined", zap.String("worker_id", workerID))
	return nil
}

// quarantineWorkerUnsafe quarantines a worker without acquiring the lock
// This should only be called when the lock is already held or in safe contexts
func (s *Service) quarantineWorkerUnsafe(ctx context.Context, workerID string, reason string) error {
	s.quarantinedWorkers[workerID] = &QuarantineInfo{
		WorkerID:      workerID,
		Reason:        reason,
		QuarantinedAt: time.Now().Unix(),
		ErrorCount:    1,
	}

	if err := s.storage.SaveQuarantinedWorkers(ctx, s.quarantinedWorkers); err != nil {
		logger.Warn("Failed to save quarantined workers list", zap.Error(err))
	}

	logger.Info("Worker quarantined",
		zap.String("worker_id", workerID),
		zap.String("reason", reason),
	)

	return nil
}
