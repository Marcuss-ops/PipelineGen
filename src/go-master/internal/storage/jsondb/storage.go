// Package jsondb implementa lo storage usando file JSON (compatibilità con Python)
package jsondb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"velox/go-master/internal/core/worker"
	"velox/go-master/internal/storage"
	"velox/go-master/pkg/models"
)

// Storage implementa storage.Storage combinando tutti gli store JSON
type Storage struct {
	jobStore     *JobStore
	workerStore  *WorkerStore
	queueStore   *QueueStore
	stateManager *StateManager
	dataDir      string
}

// NewStorage crea una nuova istanza di Storage JSON
func NewStorage(dataDir string) (*Storage, error) {
	// Crea directory principale
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	// Crea sotto-directory
	backupDir := filepath.Join(dataDir, "backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Inizializza JobStore
	jobStore, err := NewJobStore(dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create job store: %w", err)
	}

	// Inizializza WorkerStore
	workerStore, err := NewWorkerStore(dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create worker store: %w", err)
	}

	// Inizializza QueueStore
	queueStore := NewQueueStore(jobStore)

	// Inizializza StateManager
	stateManager, err := NewStateManager(dataDir, workerStore, jobStore)
	if err != nil {
		return nil, fmt.Errorf("failed to create state manager: %w", err)
	}

	return &Storage{
		jobStore:     jobStore,
		workerStore:  workerStore,
		queueStore:   queueStore,
		stateManager: stateManager,
		dataDir:      dataDir,
	}, nil
}

// JobStore methods with context

func (s *Storage) LoadQueue(ctx context.Context) (*models.Queue, error) {
	return s.jobStore.LoadQueue(ctx)
}

func (s *Storage) SaveQueue(ctx context.Context, queue *models.Queue) error {
	return s.jobStore.SaveQueue(ctx, queue)
}

func (s *Storage) GetJob(ctx context.Context, id string) (*models.Job, error) {
	return s.jobStore.GetJob(ctx, id)
}

func (s *Storage) SaveJob(ctx context.Context, job *models.Job) error {
	return s.jobStore.SaveJob(ctx, job)
}

func (s *Storage) DeleteJob(ctx context.Context, id string) error {
	return s.jobStore.DeleteJob(ctx, id)
}

func (s *Storage) ListJobs(ctx context.Context, filter models.JobFilter) ([]*models.Job, error) {
	return s.jobStore.ListJobs(ctx, filter)
}

func (s *Storage) GetNextPendingJob(ctx context.Context) (*models.Job, error) {
	return s.jobStore.GetNextPendingJob(ctx)
}

func (s *Storage) UpdateJobStatus(ctx context.Context, id string, status models.JobStatus, errorMsg string) error {
	return s.jobStore.UpdateJobStatus(ctx, id, status, errorMsg)
}

func (s *Storage) AssignJobToWorker(ctx context.Context, jobID, workerID string) error {
	return s.jobStore.AssignJobToWorker(ctx, jobID, workerID)
}

func (s *Storage) CompleteJob(ctx context.Context, jobID string, result *models.JobResult) error {
	return s.jobStore.CompleteJob(ctx, jobID, result)
}

func (s *Storage) IncrementJobRetries(ctx context.Context, jobID string) error {
	return s.jobStore.IncrementJobRetries(ctx, jobID)
}

func (s *Storage) LogJobEvent(ctx context.Context, event *models.JobEvent) error {
	return s.jobStore.LogJobEvent(ctx, event)
}

func (s *Storage) GetJobEvents(ctx context.Context, jobID string, limit int) ([]*models.JobEvent, error) {
	return s.jobStore.GetJobEvents(ctx, jobID, limit)
}

// WorkerStore methods with context

func (s *Storage) LoadWorkers(ctx context.Context) (map[string]*models.Worker, error) {
	return s.workerStore.LoadWorkers(ctx)
}

func (s *Storage) SaveWorkers(ctx context.Context, workers map[string]*models.Worker) error {
	return s.workerStore.SaveWorkers(ctx, workers)
}

func (s *Storage) GetWorker(ctx context.Context, id string) (*models.Worker, error) {
	return s.workerStore.GetWorker(ctx, id)
}

func (s *Storage) SaveWorker(ctx context.Context, worker *models.Worker) error {
	return s.workerStore.SaveWorker(ctx, worker)
}

func (s *Storage) DeleteWorker(ctx context.Context, id string) error {
	return s.workerStore.DeleteWorker(ctx, id)
}

func (s *Storage) GetActiveWorkers(ctx context.Context, timeout time.Duration) (map[string]*models.Worker, error) {
	return s.workerStore.GetActiveWorkers(ctx, timeout)
}

func (s *Storage) GetWorkersByCapability(ctx context.Context, cap models.WorkerCapability) ([]*models.Worker, error) {
	return s.workerStore.GetWorkersByCapability(ctx, cap)
}

func (s *Storage) UpdateWorkerStatus(ctx context.Context, id string, status models.WorkerStatus) error {
	return s.workerStore.UpdateWorkerStatus(ctx, id, status)
}

func (s *Storage) UpdateWorkerJob(ctx context.Context, workerID, jobID string) error {
	return s.workerStore.UpdateWorkerJob(ctx, workerID, jobID)
}

func (s *Storage) TouchWorker(ctx context.Context, id string) error {
	return s.workerStore.TouchWorker(ctx, id)
}

func (s *Storage) GetAvailableWorkers(ctx context.Context) ([]*models.Worker, error) {
	return s.workerStore.GetAvailableWorkers(ctx)
}

// QueueStore methods with context

func (s *Storage) GetQueueStats(ctx context.Context) (storage.QueueStats, error) {
	return s.queueStore.GetQueueStats(ctx)
}

func (s *Storage) GetJobsByStatus(ctx context.Context, status models.JobStatus) ([]*models.Job, error) {
	return s.queueStore.GetJobsByStatus(ctx, status)
}

func (s *Storage) GetJobsByWorker(ctx context.Context, workerID string) ([]*models.Job, error) {
	return s.queueStore.GetJobsByWorker(ctx, workerID)
}

func (s *Storage) RequeueFailedJobs(ctx context.Context) (int, error) {
	return s.queueStore.RequeueFailedJobs(ctx)
}

func (s *Storage) CleanupOldJobs(ctx context.Context, before time.Time) (int, error) {
	return s.queueStore.CleanupOldJobs(ctx, before)
}

func (s *Storage) ArchiveCompletedJobs(ctx context.Context, before time.Time) (int, error) {
	return s.queueStore.ArchiveCompletedJobs(ctx, before)
}

// StateManager methods with context

func (s *Storage) GetActiveWorkersMap(ctx context.Context) map[string]*models.Worker {
	return s.stateManager.GetActiveWorkers(ctx)
}

func (s *Storage) UpdateWorkerStatusGlobal(ctx context.Context, id string, status models.WorkerStatus) error {
	return s.stateManager.UpdateWorkerStatus(ctx, id, status)
}

func (s *Storage) GetWorkerStatusState(ctx context.Context, id string) (models.WorkerStatus, error) {
	return s.stateManager.GetWorkerStatus(ctx, id)
}

func (s *Storage) AcquireLock(ctx context.Context, name string, ttl time.Duration) (storage.Lock, error) {
	return s.stateManager.AcquireLock(ctx, name, ttl)
}

func (s *Storage) GetGlobalState(ctx context.Context) (*storage.GlobalState, error) {
	return s.stateManager.GetGlobalState(ctx)
}

func (s *Storage) UpdateGlobalState(ctx context.Context, state *storage.GlobalState) error {
	return s.stateManager.UpdateGlobalState(ctx, state)
}

func (s *Storage) Watch(ctx context.Context) (<-chan storage.StateChange, error) {
	return s.stateManager.Watch(ctx)
}

// Storage interface methods

func (s *Storage) Close() error {
	// JSON storage non ha connessioni da chiudere
	return nil
}

func (s *Storage) HealthCheck() error {
	// Verifica che i file siano accessibili
	if _, err := os.Stat(s.dataDir); err != nil {
		return fmt.Errorf("data directory not accessible: %w", err)
	}

	// Verifica che possiamo leggere la coda
	ctx := context.Background()
	if _, err := s.jobStore.LoadQueue(ctx); err != nil {
		return fmt.Errorf("cannot load queue: %w", err)
	}

	// Verifica che possiamo leggere i worker
	if _, err := s.workerStore.LoadWorkers(ctx); err != nil {
		return fmt.Errorf("cannot load workers: %w", err)
	}

	return nil
}

func (s *Storage) Backup(dest string) error {
	// Crea directory di backup se non esiste
	if err := os.MkdirAll(dest, 0755); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	timestamp := time.Now().Format("20060102_150405")
	backupDir := filepath.Join(dest, fmt.Sprintf("backup_%s", timestamp))

	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("failed to create timestamped backup directory: %w", err)
	}

	// Copia queue.json
	queueSrc := filepath.Join(s.dataDir, "queue.json")
	queueDst := filepath.Join(backupDir, "queue.json")
	if err := copyFile(queueSrc, queueDst); err != nil {
		return fmt.Errorf("failed to backup queue: %w", err)
	}

	// Copia workers.json
	workersSrc := filepath.Join(s.dataDir, "workers.json")
	workersDst := filepath.Join(backupDir, "workers.json")
	if err := copyFile(workersSrc, workersDst); err != nil {
		return fmt.Errorf("failed to backup workers: %w", err)
	}

	// Copia global_state.json
	stateSrc := filepath.Join(s.dataDir, "global_state.json")
	stateDst := filepath.Join(backupDir, "global_state.json")
	if err := copyFile(stateSrc, stateDst); err != nil {
		return fmt.Errorf("failed to backup global state: %w", err)
	}

	return nil
}

func (s *Storage) Restore(src string) error {
	// Verifica che il backup esista
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("backup not found: %w", err)
	}

	// Copia queue.json
	queueSrc := filepath.Join(src, "queue.json")
	queueDst := filepath.Join(s.dataDir, "queue.json")
	if err := copyFile(queueSrc, queueDst); err != nil {
		return fmt.Errorf("failed to restore queue: %w", err)
	}

	// Copia workers.json
	workersSrc := filepath.Join(src, "workers.json")
	workersDst := filepath.Join(s.dataDir, "workers.json")
	if err := copyFile(workersSrc, workersDst); err != nil {
		return fmt.Errorf("failed to restore workers: %w", err)
	}

	// Copia global_state.json
	stateSrc := filepath.Join(src, "global_state.json")
	stateDst := filepath.Join(s.dataDir, "global_state.json")
	if err := copyFile(stateSrc, stateDst); err != nil {
		return fmt.Errorf("failed to restore global state: %w", err)
	}

	// Ricarica i dati
	if err := s.jobStore.loadQueueFromDisk(); err != nil {
		return fmt.Errorf("failed to reload queue: %w", err)
	}

	if err := s.workerStore.loadRegistryFromDisk(); err != nil {
		return fmt.Errorf("failed to reload workers: %w", err)
	}

	if err := s.stateManager.loadGlobalState(); err != nil {
		return fmt.Errorf("failed to reload global state: %w", err)
	}

	return nil
}

// copyFile copia un file da src a dst
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	return os.WriteFile(dst, data, 0644)
}

// GetDataDir restituisce la directory dei dati
func (s *Storage) GetDataDir() string {
	return s.dataDir
}

// GetJobStore restituisce il JobStore sottostante
func (s *Storage) GetJobStore() *JobStore {
	return s.jobStore
}

// GetWorkerStore restituisce il WorkerStore sottostante
func (s *Storage) GetWorkerStore() *WorkerStore {
	return s.workerStore
}
func (s *Storage) SaveWorkerCommand(ctx context.Context, command *models.WorkerCommand) error {
	commands, err := s.loadWorkerCommands(ctx, command.WorkerID)
	if err != nil {
		return fmt.Errorf("failed to load commands: %w", err)
	}
	commands = append(commands, command)
	return s.saveWorkerCommands(ctx, command.WorkerID, commands)
}

func (s *Storage) GetWorkerCommands(ctx context.Context, workerID string) ([]*models.WorkerCommand, error) {
	commands, err := s.loadWorkerCommands(ctx, workerID)
	if err != nil {
		return nil, fmt.Errorf("failed to load commands: %w", err)
	}
	// Return only pending commands
	var pending []*models.WorkerCommand
	for _, cmd := range commands {
		if !cmd.Acknowledged {
			pending = append(pending, cmd)
		}
	}
	return pending, nil
}

func (s *Storage) AckWorkerCommand(ctx context.Context, commandID string) error {
	// We need to scan all workers to find the command
	workers, err := s.workerStore.LoadWorkers(ctx)
	if err != nil {
		return fmt.Errorf("failed to load workers: %w", err)
	}
	for workerID := range workers {
		commands, err := s.loadWorkerCommands(ctx, workerID)
		if err != nil {
			continue
		}
		for i, cmd := range commands {
			if cmd.ID == commandID {
				commands[i].Acknowledged = true
				return s.saveWorkerCommands(ctx, workerID, commands)
			}
		}
	}
	return fmt.Errorf("command %s not found", commandID)
}

func (s *Storage) LoadRevokedWorkers(ctx context.Context) (map[string]bool, error) {
	data, err := os.ReadFile(s.revokedWorkersPath())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	var revoked map[string]bool
	if err := json.Unmarshal(data, &revoked); err != nil {
		return map[string]bool{}, nil
	}
	return revoked, nil
}

func (s *Storage) SaveRevokedWorkers(ctx context.Context, revoked map[string]bool) error {
	data, err := json.Marshal(revoked)
	if err != nil {
		return err
	}
	return atomicWrite(s.revokedWorkersPath(), data)
}

func (s *Storage) LoadQuarantinedWorkers(ctx context.Context) (map[string]*worker.QuarantineInfo, error) {
	data, err := os.ReadFile(s.quarantinedWorkersPath())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]*worker.QuarantineInfo{}, nil
		}
		return nil, err
	}
	var quarantined map[string]*worker.QuarantineInfo
	if err := json.Unmarshal(data, &quarantined); err != nil {
		return map[string]*worker.QuarantineInfo{}, nil
	}
	return quarantined, nil
}

func (s *Storage) SaveQuarantinedWorkers(ctx context.Context, quarantined map[string]*worker.QuarantineInfo) error {
	data, err := json.Marshal(quarantined)
	if err != nil {
		return err
	}
	return atomicWrite(s.quarantinedWorkersPath(), data)
}

// Helper methods for worker commands

func (s *Storage) loadWorkerCommands(ctx context.Context, workerID string) ([]*models.WorkerCommand, error) {
	path := filepath.Join(s.dataDir, "commands_"+workerID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []*models.WorkerCommand{}, nil
		}
		return nil, err
	}
	var commands []*models.WorkerCommand
	if err := json.Unmarshal(data, &commands); err != nil {
		return []*models.WorkerCommand{}, nil
	}
	return commands, nil
}

func (s *Storage) saveWorkerCommands(ctx context.Context, workerID string, commands []*models.WorkerCommand) error {
	data, err := json.Marshal(commands)
	if err != nil {
		return err
	}
	path := filepath.Join(s.dataDir, "commands_"+workerID+".json")
	return atomicWrite(path, data)
}

// Helper path methods

func (s *Storage) revokedWorkersPath() string {
	return filepath.Join(s.dataDir, "revoked_workers.json")
}

func (s *Storage) quarantinedWorkersPath() string {
	return filepath.Join(s.dataDir, "quarantined_workers.json")
}

// atomicWrite writes data to a file atomically using a temp file + rename
func atomicWrite(path string, data []byte) error {
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
