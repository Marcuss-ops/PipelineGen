package worker

import (
	"context"
	"fmt"
	"time"

	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/models"
	"go.uber.org/zap"
)

func (s *Service) UpdateWorkerStatus(ctx context.Context, heartbeat *models.WorkerHeartbeat) error {
	s.mu.Lock()
	worker, exists := s.workers[heartbeat.WorkerID]
	if !exists {
		s.mu.Unlock()
		return ErrWorkerNotFound
	}

	if s.revokedWorkers[worker.ID] {
		s.mu.Unlock()
		return ErrWorkerRevoked
	}
	if quarantine, exists := s.quarantinedWorkers[worker.ID]; exists {
		s.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrWorkerQuarantined, quarantine.Reason)
	}

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

	if len(heartbeat.Logs) > 0 {
		s.processWorkerLogs(workerClone.ID, heartbeat.Logs)
	}

	if len(heartbeat.Errors) > 0 {
		s.processWorkerErrors(ctx, workerClone.ID, heartbeat.Errors)
	}

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

func (s *Service) CheckOfflineWorkers() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	timeout := time.Duration(s.cfg.Workers.HeartbeatTimeout) * time.Second
	var offline []string

	for id, worker := range s.workers {
		if worker.Status == models.WorkerOffline {
			continue
		}

		if now.Sub(worker.LastHeartbeat) > timeout {
			workerClone := worker.Clone()
			workerClone.Status = models.WorkerOffline
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

func (s *Service) IsWorkerRevoked(workerID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revokedWorkers[workerID]
}

func (s *Service) IsWorkerQuarantined(workerID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.quarantinedWorkers[workerID]
	return exists
}

func (s *Service) IsWorkerSchedulable(worker *models.Worker) bool {
	if worker.Status != models.WorkerIdle {
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

func (s *Service) GetWorkerStats() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var online, offline, busy, errorCount int
	for _, worker := range s.workers {
		switch worker.Status {
		case models.WorkerIdle:
			online++
		case models.WorkerOffline:
			offline++
		case models.WorkerBusy:
			busy++
		case models.WorkerError:
			errorCount++
		}
	}

	return map[string]interface{}{
		"total":       len(s.workers),
		"online":      online,
		"offline":     offline,
		"busy":        busy,
		"error":       errorCount,
		"revoked":     len(s.revokedWorkers),
		"quarantined": len(s.quarantinedWorkers),
	}
}
