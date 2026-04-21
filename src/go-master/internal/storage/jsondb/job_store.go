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

// JobStore implementa storage.JobStore usando file JSON
type JobStore struct {
	dataDir string
	mutex   sync.RWMutex
	queue   *models.Queue
}

// NewJobStore crea un nuovo JobStore JSON
func NewJobStore(dataDir string) (*JobStore, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	store := &JobStore{
		dataDir: dataDir,
		queue: &models.Queue{
			Jobs:      make([]*models.Job, 0),
			UpdatedAt: time.Now(),
			Version:   1,
		},
	}

	// Prova a caricare la coda esistente
	if err := store.loadQueueFromDisk(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load queue: %w", err)
	}

	return store, nil
}

// LoadQueue carica la coda completa dei job
func (s *JobStore) LoadQueue(ctx context.Context) (*models.Queue, error) {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	s.mutex.RLock()
	defer s.mutex.RUnlock()

	// Ritorna una copia
	queueCopy := &models.Queue{
		Jobs:      make([]*models.Job, len(s.queue.Jobs)),
		UpdatedAt: s.queue.UpdatedAt,
		Version:   s.queue.Version,
	}
	for i, job := range s.queue.Jobs {
		queueCopy.Jobs[i] = job.Clone()
	}

	return queueCopy, nil
}

// SaveQueue salva la coda completa dei job
func (s *JobStore) SaveQueue(ctx context.Context, queue *models.Queue) error {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.queue = &models.Queue{
		Jobs:      make([]*models.Job, len(queue.Jobs)),
		UpdatedAt: time.Now(),
		Version:   queue.Version + 1,
	}
	for i, job := range queue.Jobs {
		s.queue.Jobs[i] = job.Clone()
	}

	return s.saveQueueToDisk()
}

// GetJob recupera un job per ID
func (s *JobStore) GetJob(ctx context.Context, id string) (*models.Job, error) {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	s.mutex.RLock()
	defer s.mutex.RUnlock()

	for _, job := range s.queue.Jobs {
		if job.ID == id {
			return job.Clone(), nil
		}
	}

	return nil, fmt.Errorf("job not found: %s", id)
}

// SaveJob salva un job (crea o aggiorna)
func (s *JobStore) SaveJob(ctx context.Context, job *models.Job) error {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Cerca se il job esiste già
	found := false
	for i, j := range s.queue.Jobs {
		if j.ID == job.ID {
			s.queue.Jobs[i] = job.Clone()
			found = true
			break
		}
	}

	// Se non esiste, aggiungilo
	if !found {
		s.queue.Jobs = append(s.queue.Jobs, job.Clone())
	}

	s.queue.UpdatedAt = time.Now()
	s.queue.Version++

	return s.saveQueueToDisk()
}

// DeleteJob elimina un job per ID
func (s *JobStore) DeleteJob(ctx context.Context, id string) error {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	for i, job := range s.queue.Jobs {
		if job.ID == id {
			// Rimuovi il job dallo slice
			s.queue.Jobs = append(s.queue.Jobs[:i], s.queue.Jobs[i+1:]...)
			s.queue.UpdatedAt = time.Now()
			s.queue.Version++
			return s.saveQueueToDisk()
		}
	}

	return fmt.Errorf("job not found: %s", id)
}

// ListJobs restituisce una lista di job filtrati
func (s *JobStore) ListJobs(ctx context.Context, filter models.JobFilter) ([]*models.Job, error) {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	s.mutex.RLock()
	defer s.mutex.RUnlock()

	var result []*models.Job

	for _, job := range s.queue.Jobs {
		// Applica filtri
		if filter.Status != nil && job.Status != *filter.Status {
			continue
		}
		if filter.Type != nil && job.Type != *filter.Type {
			continue
		}
		if filter.WorkerID != "" && job.WorkerID != filter.WorkerID {
			continue
		}

		result = append(result, job.Clone())
	}

	// Applica ordinamento per priorità e data di creazione
	sort.Slice(result, func(i, j int) bool {
		if result[i].Priority != result[j].Priority {
			return result[i].Priority > result[j].Priority
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})

	// Applica paginazione
	if filter.Offset > 0 {
		if filter.Offset >= len(result) {
			return []*models.Job{}, nil
		}
		result = result[filter.Offset:]
	}

	if filter.Limit > 0 && filter.Limit < len(result) {
		result = result[:filter.Limit]
	}

	return result, nil
}

// GetNextPendingJob restituisce il prossimo job in stato pending
func (s *JobStore) GetNextPendingJob(ctx context.Context) (*models.Job, error) {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	s.mutex.RLock()
	defer s.mutex.RUnlock()

	// Cerca job pending ordinati per priorità
	var pendingJobs []*models.Job
	for _, job := range s.queue.Jobs {
		if job.Status == models.StatusPending {
			pendingJobs = append(pendingJobs, job)
		}
	}

	if len(pendingJobs) == 0 {
		return nil, nil
	}

	// Ordina per priorità decrescente e poi per data di creazione
	sort.Slice(pendingJobs, func(i, j int) bool {
		if pendingJobs[i].Priority != pendingJobs[j].Priority {
			return pendingJobs[i].Priority > pendingJobs[j].Priority
		}
		return pendingJobs[i].CreatedAt.Before(pendingJobs[j].CreatedAt)
	})

	return pendingJobs[0].Clone(), nil
}

// UpdateJobStatus aggiorna lo stato di un job
func (s *JobStore) UpdateJobStatus(ctx context.Context, id string, status models.JobStatus, errorMsg string) error {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	for _, job := range s.queue.Jobs {
		if job.ID == id {
			job.Status = status
			job.Error = errorMsg
			job.UpdatedAt = time.Now()

			if status == models.StatusProcessing && job.StartedAt == nil {
				now := time.Now()
				job.StartedAt = &now
			}

			if status.IsTerminal() && job.CompletedAt == nil {
				now := time.Now()
				job.CompletedAt = &now
			}

			s.queue.UpdatedAt = time.Now()
			s.queue.Version++
			return s.saveQueueToDisk()
		}
	}

	return fmt.Errorf("job not found: %s", id)
}

// AssignJobToWorker assegna un job a un worker
func (s *JobStore) AssignJobToWorker(ctx context.Context, jobID, workerID string) error {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	for _, job := range s.queue.Jobs {
		if job.ID == jobID {
			job.WorkerID = workerID
			job.Status = models.StatusProcessing
			job.UpdatedAt = time.Now()
			now := time.Now()
			job.StartedAt = &now

			s.queue.UpdatedAt = time.Now()
			s.queue.Version++
			return s.saveQueueToDisk()
		}
	}

	return fmt.Errorf("job not found: %s", jobID)
}

// CompleteJob marca un job come completato con il risultato
func (s *JobStore) CompleteJob(ctx context.Context, jobID string, result *models.JobResult) error {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	for _, job := range s.queue.Jobs {
		if job.ID == jobID {
			job.Status = models.StatusCompleted
			// Converti JobResult in map[string]interface{}
			if result != nil {
				job.Result = map[string]interface{}{
					"success":       result.Success,
					"output_path":   result.OutputPath,
					"video_url":     result.VideoURL,
					"drive_file_id": result.DriveFileID,
					"youtube_id":    result.YouTubeID,
					"metadata":      result.Metadata,
					"completed_at":  result.CompletedAt,
				}
			}
			job.UpdatedAt = time.Now()
			now := time.Now()
			job.CompletedAt = &now

			s.queue.UpdatedAt = time.Now()
			s.queue.Version++
			return s.saveQueueToDisk()
		}
	}

	return fmt.Errorf("job not found: %s", jobID)
}

// IncrementJobRetries incrementa il contatore dei retry di un job
func (s *JobStore) IncrementJobRetries(ctx context.Context, jobID string) error {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	for _, job := range s.queue.Jobs {
		if job.ID == jobID {
			job.Retries++
			job.UpdatedAt = time.Now()

			s.queue.UpdatedAt = time.Now()
			s.queue.Version++
			return s.saveQueueToDisk()
		}
	}

	return fmt.Errorf("job not found: %s", jobID)
}

// LogJobEvent registra un evento per un job
func (s *JobStore) LogJobEvent(ctx context.Context, event *models.JobEvent) error {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	events, err := s.loadJobEvents(event.JobID)
	if err != nil {
		return fmt.Errorf("failed to load events: %w", err)
	}
	events = append(events, event)
	return s.saveJobEvents(event.JobID, events)
}

// GetJobEvents restituisce gli eventi di un job
func (s *JobStore) GetJobEvents(ctx context.Context, jobID string, limit int) ([]*models.JobEvent, error) {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	events, err := s.loadJobEvents(jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to load events: %w", err)
	}
	if limit <= 0 || limit >= len(events) {
		return events, nil
	}
	return events[len(events)-limit:], nil
}

// loadJobEvents carica gli eventi di un job dal disco
func (s *JobStore) loadJobEvents(jobID string) ([]*models.JobEvent, error) {
	path := filepath.Join(s.dataDir, "events_"+jobID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []*models.JobEvent{}, nil
		}
		return nil, err
	}
	var events []*models.JobEvent
	if err := json.Unmarshal(data, &events); err != nil {
		return []*models.JobEvent{}, nil
	}
	return events, nil
}

// saveJobEvents salva gli eventi di un job su disco
func (s *JobStore) saveJobEvents(jobID string, events []*models.JobEvent) error {
	data, err := json.Marshal(events)
	if err != nil {
		return err
	}
	path := filepath.Join(s.dataDir, "events_"+jobID+".json")
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// loadQueueFromDisk carica la coda dal file JSON
func (s *JobStore) loadQueueFromDisk() error {
	filepath := filepath.Join(s.dataDir, "queue.json")

	data, err := os.ReadFile(filepath)
	if err != nil {
		return err
	}

	var queue models.Queue
	if err := json.Unmarshal(data, &queue); err != nil {
		return fmt.Errorf("failed to unmarshal queue: %w", err)
	}

	s.queue = &queue
	return nil
}

// saveQueueToDisk salva la coda nel file JSON
func (s *JobStore) saveQueueToDisk() error {
	filepath := filepath.Join(s.dataDir, "queue.json")

	// Serializza con indentazione per leggibilità
	data, err := json.MarshalIndent(s.queue, "", "  ")
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
		// Try to clean up the temp file
		if removeErr := os.Remove(tempFile); removeErr != nil {
			fmt.Printf("Warning: failed to remove temp file %s: %v\n", tempFile, removeErr)
		}
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// GetDataDir restituisce la directory dei dati
func (s *JobStore) GetDataDir() string {
	return s.dataDir
}