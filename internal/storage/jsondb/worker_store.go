// Package jsondb implementa lo storage usando file JSON (compatibilità con Python)
package jsondb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"velox/go-master/pkg/models"
)

// WorkerStore implementa storage.WorkerStore usando file JSON
type WorkerStore struct {
	dataDir  string
	mutex    sync.RWMutex
	registry *models.WorkerRegistry
}

// NewWorkerStore crea un nuovo WorkerStore JSON
func NewWorkerStore(dataDir string) (*WorkerStore, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	store := &WorkerStore{
		dataDir: dataDir,
		registry: &models.WorkerRegistry{
			Workers:   make(map[string]*models.Worker),
			UpdatedAt: time.Now(),
			Version:   1,
		},
	}

	// Prova a caricare il registro esistente
	if err := store.loadRegistryFromDisk(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load worker registry: %w", err)
	}

	return store, nil
}

// LoadWorkers carica tutti i worker registrati
func (s *WorkerStore) LoadWorkers(ctx context.Context) (map[string]*models.Worker, error) {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	s.mutex.RLock()
	defer s.mutex.RUnlock()

	// Ritorna una copia
	result := make(map[string]*models.Worker)
	for id, worker := range s.registry.Workers {
		result[id] = worker.Clone()
	}

	return result, nil
}

// SaveWorkers salva tutti i worker
func (s *WorkerStore) SaveWorkers(ctx context.Context, workers map[string]*models.Worker) error {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.registry.Workers = make(map[string]*models.Worker)
	for id, worker := range workers {
		s.registry.Workers[id] = worker.Clone()
	}

	s.registry.UpdatedAt = time.Now()
	s.registry.Version++

	return s.saveRegistryToDisk()
}

// GetWorker recupera un worker per ID
func (s *WorkerStore) GetWorker(ctx context.Context, id string) (*models.Worker, error) {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	s.mutex.RLock()
	defer s.mutex.RUnlock()

	worker, exists := s.registry.Workers[id]
	if !exists {
		return nil, fmt.Errorf("worker not found: %s", id)
	}

	return worker.Clone(), nil
}

// SaveWorker salva un worker (crea o aggiorna)
func (s *WorkerStore) SaveWorker(ctx context.Context, worker *models.Worker) error {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	workerCopy := worker.Clone()
	workerCopy.UpdatedAt = time.Now()

	s.registry.Workers[worker.ID] = workerCopy
	s.registry.UpdatedAt = time.Now()
	s.registry.Version++

	return s.saveRegistryToDisk()
}

// DeleteWorker elimina un worker per ID
func (s *WorkerStore) DeleteWorker(ctx context.Context, id string) error {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	if _, exists := s.registry.Workers[id]; !exists {
		return fmt.Errorf("worker not found: %s", id)
	}

	delete(s.registry.Workers, id)
	s.registry.UpdatedAt = time.Now()
	s.registry.Version++

	return s.saveRegistryToDisk()
}

// GetActiveWorkers restituisce tutti i worker attivi (online)
func (s *WorkerStore) GetActiveWorkers(ctx context.Context, timeout time.Duration) (map[string]*models.Worker, error) {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	s.mutex.RLock()
	defer s.mutex.RUnlock()

	result := make(map[string]*models.Worker)
	now := time.Now()

	for id, worker := range s.registry.Workers {
		if now.Sub(worker.LastSeen) < timeout {
			result[id] = worker.Clone()
		}
	}

	return result, nil
}

// GetWorkersByCapability restituisce i worker con una specifica capability
func (s *WorkerStore) GetWorkersByCapability(ctx context.Context, cap models.WorkerCapability) ([]*models.Worker, error) {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	s.mutex.RLock()
	defer s.mutex.RUnlock()

	var result []*models.Worker

	for _, worker := range s.registry.Workers {
		if worker.HasCapability(cap) {
			result = append(result, worker.Clone())
		}
	}

	// Ordina per numero di job completati (più esperienza prima)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Stats.JobsCompleted > result[j].Stats.JobsCompleted
	})

	return result, nil
}

// UpdateWorkerStatus aggiorna lo stato di un worker
func (s *WorkerStore) UpdateWorkerStatus(ctx context.Context, id string, status models.WorkerStatus) error {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	worker, exists := s.registry.Workers[id]
	if !exists {
		return fmt.Errorf("worker not found: %s", id)
	}

	worker.Status = status
	worker.UpdatedAt = time.Now()

	s.registry.UpdatedAt = time.Now()
	s.registry.Version++

	return s.saveRegistryToDisk()
}

// UpdateWorkerJob assegna o rimuove un job da un worker
func (s *WorkerStore) UpdateWorkerJob(ctx context.Context, workerID, jobID string) error {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	worker, exists := s.registry.Workers[workerID]
	if !exists {
		return fmt.Errorf("worker not found: %s", workerID)
	}

	worker.CurrentJobID = jobID
	worker.UpdatedAt = time.Now()

	if jobID != "" {
		worker.Status = models.WorkerBusy
		now := time.Now()
		worker.Stats.LastJobStarted = &now
	} else {
		worker.Status = models.WorkerIdle
	}

	s.registry.UpdatedAt = time.Now()
	s.registry.Version++

	return s.saveRegistryToDisk()
}

// TouchWorker aggiorna il timestamp last_seen di un worker
func (s *WorkerStore) TouchWorker(ctx context.Context, id string) error {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	worker, exists := s.registry.Workers[id]
	if !exists {
		return fmt.Errorf("worker not found: %s", id)
	}

	worker.LastSeen = time.Now()
	worker.UpdatedAt = time.Now()

	s.registry.UpdatedAt = time.Now()

	return s.saveRegistryToDisk()
}

// GetAvailableWorkers restituisce i worker disponibili per accettare job
func (s *WorkerStore) GetAvailableWorkers(ctx context.Context) ([]*models.Worker, error) {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	s.mutex.RLock()
	defer s.mutex.RUnlock()

	var result []*models.Worker

	for _, worker := range s.registry.Workers {
		if worker.CanAcceptJob() {
			result = append(result, worker.Clone())
		}
	}

	// Ordina per numero di job completati (worker più affidabili prima)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Stats.JobsCompleted > result[j].Stats.JobsCompleted
	})

	return result, nil
}

// GetAllWorkers restituisce tutti i worker (copia)
func (s *WorkerStore) GetAllWorkers() []*models.Worker {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	var result []*models.Worker
	for _, worker := range s.registry.Workers {
		result = append(result, worker.Clone())
	}

	// Ordina per data di registrazione
	sort.Slice(result, func(i, j int) bool {
		return result[i].RegisteredAt.Before(result[j].RegisteredAt)
	})

	return result
}

// GetWorkerCount restituisce il numero totale di worker
func (s *WorkerStore) GetWorkerCount() int {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	return len(s.registry.Workers)
}

// GetOnlineWorkerCount restituisce il numero di worker online
func (s *WorkerStore) GetOnlineWorkerCount(timeout time.Duration) int {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	count := 0
	now := time.Now()

	for _, worker := range s.registry.Workers {
		if now.Sub(worker.LastSeen) < timeout {
			count++
		}
	}

	return count
}

// loadRegistryFromDisk carica il registro dal file JSON
func (s *WorkerStore) loadRegistryFromDisk() error {
	filepath := filepath.Join(s.dataDir, "workers.json")

	data, err := os.ReadFile(filepath)
	if err != nil {
		return err
	}

	var registry models.WorkerRegistry
	if err := json.Unmarshal(data, &registry); err != nil {
		return fmt.Errorf("failed to unmarshal worker registry: %w", err)
	}

	s.registry = &registry
	return nil
}

// saveRegistryToDisk salva il registro nel file JSON
func (s *WorkerStore) saveRegistryToDisk() error {
	filepath := filepath.Join(s.dataDir, "workers.json")

	// Serializza con indentazione per leggibilità
	data, err := json.MarshalIndent(s.registry, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal worker registry: %w", err)
	}

	// Scrivi su file temporaneo per atomicità
	tempFile := filepath + ".tmp"
	if err := os.WriteFile(tempFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Rinomina atomicamente
	if err := os.Rename(tempFile, filepath); err != nil {
		os.Remove(tempFile)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}