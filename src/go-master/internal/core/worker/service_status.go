package worker

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/models"
)

// UpdateWorkerStatus updates worker status from heartbeat
func (s *Service) UpdateWorkerStatus(ctx context.Context, heartbeat *models.WorkerHeartbeat) error {
	s.mu.Lock()
	worker, exists := s.workers[heartbeat.WorkerID]
	if !exists {
		s.mu.Unlock()
		return ErrWorkerNotFound
	}

	// Check if worker is revoked
	if s.revokedWorkers[worker.ID] {
		s.mu.Unlock()
		return ErrWorkerRevoked
	}
	if quarantine, exists := s.quarantinedWorkers[worker.ID]; exists {
		s.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrWorkerQuarantined, quarantine.Reason)
	}

	// Clone worker to avoid mutating shared pointer
	workerClone := worker.Clone()
	s.mu.Unlock()

	now := time.Now()
	workerClone.Status = heartbeat.Status
	workerClone.LastHeartbeat = now
	workerClone.CurrentJobID = heartbeat.CurrentJobID
	workerClone.DiskFreeGB = heartbeat.DiskFreeGB
	workerClone.MemoryFreeMB = heartbeat.MemoryFreeMB
	workerClone.CPUUsage = heartbeat.CPUUsage
	workerClone.Version = heartbeat.Version
	workerClone.CodeHash = heartbeat.CodeHash

	if heartbeat.Capabilities != nil {
		workerClone.Capabilities = heartbeat.Capabilities
	}

	// Process logs
	if len(heartbeat.Logs) > 0 {
		s.processWorkerLogs(workerClone.ID, heartbeat.Logs)
	}

	// Process errors
	if len(heartbeat.Errors) > 0 {
		s.processWorkerErrors(ctx, workerClone.ID, heartbeat.Errors)
	}

	// Update in memory under lock
	s.mu.Lock()
	s.workers[heartbeat.WorkerID] = workerClone
	s.mu.Unlock()

	if err := s.SaveWorkers(ctx); err != nil {
		return err
	}

	logger.Debug("Worker heartbeat processed",
		zap.String("worker_id", workerClone.ID),
		zap.String("status", string(workerClone.Status)),
	)

	return nil
}

// CheckOfflineWorkers marks workers as offline if heartbeat timeout
func (s *Service) CheckOfflineWorkers() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	timeout := time.Duration(s.cfg.Workers.HeartbeatTimeout) * time.Second
	var offline []string

	for id, worker := range s.workers {
		if worker.Status == models.WorkerStatusOffline {
			continue
		}

		if now.Sub(worker.LastHeartbeat) > timeout {
			// Clone and update
			workerClone := worker.Clone()
			workerClone.Status = models.WorkerStatusOffline
			s.workers[id] = workerClone
			offline = append(offline, id)
			logger.Warn("Worker marked offline",
				zap.String("worker_id", id),
				zap.Duration("last_heartbeat", now.Sub(worker.LastHeartbeat)),
			)
		}
	}

	return offline
}

// GetWorkerStats returns statistics about workers
func (s *Service) GetWorkerStats() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var online, offline, busy, errorCount int
	for _, worker := range s.workers {
		switch worker.Status {
		case models.WorkerStatusOnline:
			online++
		case models.WorkerStatusOffline:
			offline++
		case models.WorkerStatusBusy:
			busy++
		case models.WorkerError:
			errorCount++
		}
	}

	stats := map[string]interface{}{
		"total":       len(s.workers),
		"online":      online,
		"offline":     offline,
		"busy":        busy,
		"error":       errorCount,
		"revoked":     len(s.revokedWorkers),
		"quarantined": len(s.quarantinedWorkers),
	}

	return stats
}
