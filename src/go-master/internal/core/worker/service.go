package worker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
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

// Service provides worker management business logic
type Service struct {
	storage          StorageInterface
	cfg              *config.Config
	mu               sync.RWMutex
	workers          map[string]*models.Worker
	workerTokens     map[string]string // token -> workerID
	revokedWorkers   map[string]bool
	quarantinedWorkers map[string]*QuarantineInfo
	failHistory      []FailHistoryEntry
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

	// Return a clone to avoid exposing internal pointer
	return worker.Clone(), token, nil
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
		if worker.Status == models.WorkerIdle || worker.Status == models.WorkerBusy {
			workers = append(workers, worker.Clone())
		}
	}
	return workers
}

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

// GetPendingCommands returns pending commands for a worker
func (s *Service) GetPendingCommands(ctx context.Context, workerID string) ([]*models.WorkerCommand, error) {
	return s.storage.GetWorkerCommands(ctx, workerID)
}

// SendCommand sends a command to a worker
func (s *Service) SendCommand(ctx context.Context, workerID string, commandType string, payload map[string]interface{}) (*models.WorkerCommand, error) {
	worker, err := s.GetWorker(workerID)
	if err != nil {
		return nil, err
	}

	cmd := &models.WorkerCommand{
		ID:        uuid.New().String(),
		Type:      commandType,
		WorkerID:  workerID,
		Payload:   payload,
		CreatedAt: time.Now(),
	}

	if err := s.storage.SaveWorkerCommand(ctx, cmd); err != nil {
		return nil, err
	}

	logger.Info("Command sent to worker",
		zap.String("worker_id", workerID),
		zap.String("command_type", commandType),
	)

	_ = worker // Use worker for potential future validation
	return cmd, nil
}

// AckCommand acknowledges a command
func (s *Service) AckCommand(ctx context.Context, commandID string) error {
	return s.storage.AckWorkerCommand(ctx, commandID)
}

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

// CheckOfflineWorkers marks workers as offline if heartbeat timeout
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
			// Clone and update
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

// GetWorkerStats returns statistics about workers
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