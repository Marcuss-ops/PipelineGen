// Package jsondb implementa lo storage usando file JSON (compatibilità con Python)
package jsondb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"velox/go-master/internal/storage"
	"velox/go-master/pkg/models"
)

// marshalJSONIndent serializza un oggetto in JSON indentato
func marshalJSONIndent(v interface{}) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

// QueueStore implementa storage.QueueStore usando file JSON
type QueueStore struct {
	jobStore *JobStore
}

// NewQueueStore crea un nuovo QueueStore JSON
func NewQueueStore(jobStore *JobStore) *QueueStore {
	return &QueueStore{
		jobStore: jobStore,
	}
}

// GetQueueStats restituisce le statistiche della coda
func (s *QueueStore) GetQueueStats(ctx context.Context) (storage.QueueStats, error) {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return storage.QueueStats{}, ctx.Err()
	default:
	}

	s.jobStore.mutex.RLock()
	defer s.jobStore.mutex.RUnlock()

	stats := storage.QueueStats{}

	for _, job := range s.jobStore.queue.Jobs {
		stats.Total++
		switch job.Status {
		case models.StatusPending:
			stats.Pending++
		case models.StatusProcessing:
			stats.Processing++
		case models.StatusCompleted:
			stats.Completed++
		case models.StatusFailed:
			stats.Failed++
		case models.StatusCancelled:
			stats.Cancelled++
		case models.StatusPaused:
			stats.Paused++
		}
	}

	return stats, nil
}

// GetJobsByStatus restituisce tutti i job con uno specifico stato
func (s *QueueStore) GetJobsByStatus(ctx context.Context, status models.JobStatus) ([]*models.Job, error) {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	s.jobStore.mutex.RLock()
	defer s.jobStore.mutex.RUnlock()

	var result []*models.Job

	for _, job := range s.jobStore.queue.Jobs {
		if job.Status == status {
			result = append(result, job.Clone())
		}
	}

	return result, nil
}

// GetJobsByWorker restituisce tutti i job assegnati a un worker
func (s *QueueStore) GetJobsByWorker(ctx context.Context, workerID string) ([]*models.Job, error) {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	s.jobStore.mutex.RLock()
	defer s.jobStore.mutex.RUnlock()

	var result []*models.Job

	for _, job := range s.jobStore.queue.Jobs {
		if job.WorkerID == workerID {
			result = append(result, job.Clone())
		}
	}

	return result, nil
}

// RequeueFailedJobs rimette in coda i job falliti che possono essere riprovati
func (s *QueueStore) RequeueFailedJobs(ctx context.Context) (int, error) {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	s.jobStore.mutex.Lock()
	defer s.jobStore.mutex.Unlock()

	requeuedCount := 0

	for _, job := range s.jobStore.queue.Jobs {
		if job.Status == models.StatusFailed && job.CanRetry() {
			job.Status = models.StatusPending
			job.WorkerID = ""
			job.Error = ""
			job.UpdatedAt = time.Now()
			requeuedCount++
		}
	}

	if requeuedCount > 0 {
		s.jobStore.queue.UpdatedAt = time.Now()
		s.jobStore.queue.Version++
		if err := s.jobStore.saveQueueToDisk(); err != nil {
			return 0, fmt.Errorf("failed to save queue after requeue: %w", err)
		}
	}

	return requeuedCount, nil
}

// CleanupOldJobs elimina i job vecchi oltre una certa data
func (s *QueueStore) CleanupOldJobs(ctx context.Context, before time.Time) (int, error) {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	s.jobStore.mutex.Lock()
	defer s.jobStore.mutex.Unlock()

	originalCount := len(s.jobStore.queue.Jobs)
	var keptJobs []*models.Job

	for _, job := range s.jobStore.queue.Jobs {
		// Mantieni solo i job non terminali o completati dopo la data
		if !job.Status.IsTerminal() || job.CompletedAt == nil || job.CompletedAt.After(before) {
			keptJobs = append(keptJobs, job)
		}
	}

	deletedCount := originalCount - len(keptJobs)

	if deletedCount > 0 {
		s.jobStore.queue.Jobs = keptJobs
		s.jobStore.queue.UpdatedAt = time.Now()
		s.jobStore.queue.Version++
		if err := s.jobStore.saveQueueToDisk(); err != nil {
			return 0, fmt.Errorf("failed to save queue after cleanup: %w", err)
		}
	}

	return deletedCount, nil
}

// ArchiveCompletedJobs archivia i job completati in una directory separata
func (s *QueueStore) ArchiveCompletedJobs(ctx context.Context, before time.Time) (int, error) {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	s.jobStore.mutex.Lock()
	defer s.jobStore.mutex.Unlock()

	// Crea directory archive se non esiste
	archiveDir := filepath.Join(s.jobStore.dataDir, "archive")
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return 0, fmt.Errorf("failed to create archive directory: %w", err)
	}

	archiveFileName := filepath.Join(archiveDir, fmt.Sprintf("jobs_%s.json", time.Now().Format("20060102_150405")))

	var jobsToArchive []*models.Job
	var keptJobs []*models.Job

	for _, job := range s.jobStore.queue.Jobs {
		if job.Status == models.StatusCompleted && job.CompletedAt != nil && job.CompletedAt.Before(before) {
			jobsToArchive = append(jobsToArchive, job)
		} else {
			keptJobs = append(keptJobs, job)
		}
	}

	if len(jobsToArchive) == 0 {
		return 0, nil
	}

	// Salva job archiviati in file separato
	archiveQueue := &models.Queue{
		Jobs:      jobsToArchive,
		UpdatedAt: time.Now(),
		Version:   1,
	}

	if err := s.saveArchiveQueue(archiveQueue, archiveFileName); err != nil {
		return 0, fmt.Errorf("failed to save archive: %w", err)
	}

	// Aggiorna la coda principale
	s.jobStore.queue.Jobs = keptJobs
	s.jobStore.queue.UpdatedAt = time.Now()
	s.jobStore.queue.Version++

	if err := s.jobStore.saveQueueToDisk(); err != nil {
		return 0, fmt.Errorf("failed to save queue after archive: %w", err)
	}

	return len(jobsToArchive), nil
}

// saveArchiveQueue salva la coda archiviata in un file
func (s *QueueStore) saveArchiveQueue(queue *models.Queue, filepath string) error {
	return saveQueueToFile(queue, filepath)
}

// saveQueueToFile salva una coda in un file JSON
func saveQueueToFile(queue *models.Queue, filepath string) error {
	// Usa il formato compatibile con Python
	data, err := marshalJSONIndent(queue)
	if err != nil {
		return fmt.Errorf("failed to marshal queue: %w", err)
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

// GetArchivedJobsCount restituisce il numero totale di job archiviati
func (s *QueueStore) GetArchivedJobsCount() (int, error) {
	archiveDir := filepath.Join(s.jobStore.dataDir, "archive")

	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			// Conta i job nel file (simplificato)
			count++
		}
	}

	return count, nil
}

// GetJobHistory restituisce lo storico dei job (attivi + archiviati) per un periodo
func (s *QueueStore) GetJobHistory(from, to time.Time) ([]*models.Job, error) {
	var result []*models.Job

	// Aggiungi job attivi del periodo
	s.jobStore.mutex.RLock()
	for _, job := range s.jobStore.queue.Jobs {
		if job.CreatedAt.After(from) && job.CreatedAt.Before(to) {
			result = append(result, job.Clone())
		}
	}
	s.jobStore.mutex.RUnlock()

	// Aggiungi job da archivio
	archiveDir := filepath.Join(s.jobStore.dataDir, "archive")
	if entries, err := os.ReadDir(archiveDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			archivePath := filepath.Join(archiveDir, entry.Name())
			var archiveQueue models.Queue
			data, err := os.ReadFile(archivePath)
			if err != nil {
				continue
			}
			if err := json.Unmarshal(data, &archiveQueue); err != nil {
				continue
			}
			for _, job := range archiveQueue.Jobs {
				if job.CreatedAt.After(from) && job.CreatedAt.Before(to) {
					result = append(result, job.Clone())
				}
			}
		}
	}

	return result, nil
}