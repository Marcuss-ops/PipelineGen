package worker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"time"

	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/models"
	"go.uber.org/zap"
)

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

func (s *Service) generateToken() string {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		logger.Error("Failed to generate random token", zap.Error(err))
		return "fallback-token-" + time.Now().Format("20060102150405")
	}
	return hex.EncodeToString(bytes)
}

func (s *Service) isIPAllowed(ip string) bool {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false
	}

	for _, allowed := range s.cfg.Workers.AllowedIPs {
		if allowed == "*" {
			return true
		}
		if allowed == ip {
			return true
		}
		_, cidr, err := net.ParseCIDR(allowed)
		if err == nil && cidr.Contains(parsedIP) {
			return true
		}
	}
	return false
}

func (s *Service) processWorkerLogs(workerID string, logs []models.WorkerLogEntry) {
	for _, log := range logs {
		logger.Debug("Worker log",
			zap.String("worker_id", workerID),
			zap.String("level", log.Level),
			zap.String("message", log.Message),
			zap.String("job_id", log.JobID),
		)
	}
}

func (s *Service) processWorkerErrors(ctx context.Context, workerID string, errors []models.WorkerErrorEntry) {
	now := time.Now().Unix()
	window := int64(s.cfg.Workers.WorkerFailWindowSeconds)
	threshold := s.cfg.Workers.WorkerFailThreshold
	maxHistorySize := 10000

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, err := range errors {
		s.failHistory = append(s.failHistory, FailHistoryEntry{
			WorkerID:  workerID,
			Timestamp: now,
			Error:     err.Error,
			JobID:     err.JobID,
		})
	}

	var recentCount int
	var newHistory []FailHistoryEntry
	for _, entry := range s.failHistory {
		if now-entry.Timestamp <= window {
			newHistory = append(newHistory, entry)
			if entry.WorkerID == workerID {
				recentCount++
			}
		}
	}

	if len(newHistory) > maxHistorySize {
		newHistory = newHistory[len(newHistory)-maxHistorySize:]
	}

	s.failHistory = newHistory

	if recentCount >= threshold {
		logger.Warn("Worker quarantined due to excessive errors",
			zap.String("worker_id", workerID),
			zap.Int("error_count", recentCount),
		)
		s.quarantineWorkerUnsafe(ctx, workerID, fmt.Sprintf("Excessive errors: %d in %d seconds", recentCount, window))
	}
}

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
