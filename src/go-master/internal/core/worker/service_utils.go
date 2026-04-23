package worker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"time"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/models"
)

// generateToken generates a secure random token
func (s *Service) generateToken() string {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		// Should never happen, but fallback to a deterministic value
		logger.Error("Failed to generate random token", zap.Error(err))
		return "fallback-token-" + time.Now().Format("20060102150405")
	}
	return hex.EncodeToString(bytes)
}

// isIPAllowed checks if an IP is in the allowed list
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
		// Check CIDR
		_, cidr, err := net.ParseCIDR(allowed)
		if err == nil && cidr.Contains(parsedIP) {
			return true
		}
	}
	return false
}

// processWorkerLogs processes logs from a worker (keeps last N)
func (s *Service) processWorkerLogs(workerID string, logs []models.WorkerLogEntry) {
	// For now, just log them - in production, you'd store them
	for _, log := range logs {
		logger.Debug("Worker log",
			zap.String("worker_id", workerID),
			zap.String("level", log.Level),
			zap.String("message", log.Message),
			zap.String("job_id", log.JobID),
		)
	}
}

// processWorkerErrors processes errors from a worker
func (s *Service) processWorkerErrors(ctx context.Context, workerID string, errors []models.WorkerErrorEntry) {
	now := time.Now().Unix()
	window := int64(s.cfg.Workers.WorkerFailWindowSeconds)
	threshold := s.cfg.Workers.WorkerFailThreshold
	maxHistorySize := 10000 // Prevent unbounded growth

	// Add to fail history under lock
	s.mu.Lock()
	for _, err := range errors {
		s.failHistory = append(s.failHistory, FailHistoryEntry{
			WorkerID:  workerID,
			Timestamp: now,
			Error:     err.Error,
			JobID:     err.JobID,
		})
	}

	// Clean old entries and count for this worker
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

	// Enforce maximum size to prevent unbounded growth
	if len(newHistory) > maxHistorySize {
		newHistory = newHistory[len(newHistory)-maxHistorySize:]
	}

	s.failHistory = newHistory

	// Check if quarantine is needed - call unsafe version while holding lock
	if recentCount >= threshold {
		logger.Warn("Worker quarantined due to excessive errors",
			zap.String("worker_id", workerID),
			zap.Int("error_count", recentCount),
		)
		s.quarantineWorkerUnsafe(ctx, workerID, fmt.Sprintf("Excessive errors: %d in %d seconds", recentCount, window))
	}
	s.mu.Unlock()
}
