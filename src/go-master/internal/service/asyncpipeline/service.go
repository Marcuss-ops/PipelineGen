// Package asyncpipeline gestisce pipeline asincrona con Job ID + polling
package asyncpipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"velox/go-master/internal/service/scriptclips"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// PipelineJob rappresenta un job di pipeline asincrona
type PipelineJob struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"` // pending, running, completed, failed, cancelled
	Title       string     `json:"title"`
	Language    string     `json:"language"`
	Duration    int        `json:"duration"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Error       string     `json:"error,omitempty"`
	Progress    int        `json:"progress"` // 0-100

	// Progress details
	CurrentStep     string `json:"current_step,omitempty"`      // script_generation, entity_extraction, clip_search, clip_download, clip_upload, completed
	ClipsFound      int    `json:"clips_found"`
	ClipsMissing    int    `json:"clips_missing"`
	TotalClips      int    `json:"total_clips"`
	CurrentEntity   string `json:"current_entity,omitempty"`

	// Result (solo quando completed)
	Result *scriptclips.ScriptClipsResponse `json:"result,omitempty"`
}

// AsyncPipelineService gestisce l'esecuzione asincrona della pipeline
type AsyncPipelineService struct {
	service       *scriptclips.ScriptClipsService
	jobsDir       string
	mu            sync.RWMutex
	jobs          map[string]*PipelineJob
	cancelFuncs   map[string]context.CancelFunc // Per cancellare job in esecuzione
}

// NewAsyncPipelineService crea un nuovo servizio
func NewAsyncPipelineService(service *scriptclips.ScriptClipsService, dataDir string) *AsyncPipelineService {
	jobsDir := filepath.Join(dataDir, "async_pipeline_jobs")
	os.MkdirAll(jobsDir, 0755)

	svc := &AsyncPipelineService{
		service:     service,
		jobsDir:     jobsDir,
		jobs:        make(map[string]*PipelineJob),
		cancelFuncs: make(map[string]context.CancelFunc),
	}

	// Carica job esistenti
	svc.loadJobs()

	return svc
}

// StartPipeline avvia una pipeline asincrona e restituisce il job ID
func (s *AsyncPipelineService) StartPipeline(req *scriptclips.ScriptClipsRequest) (string, error) {
	// Genera ID univoco
	jobID := fmt.Sprintf("pipeline_%d", time.Now().UnixNano())

	// Crea job
	job := &PipelineJob{
		ID:        jobID,
		Status:    "pending",
		Title:     req.Title,
		Language:  req.Language,
		Duration:  req.Duration,
		CreatedAt: time.Now(),
		Progress:  0,
	}

	s.mu.Lock()
	s.jobs[jobID] = job
	s.mu.Unlock()

	// Salva su disco
	s.saveJob(job)

	// Avvia in background
	go s.executePipeline(jobID, req)

	logger.Info("Pipeline job started",
		zap.String("job_id", jobID),
		zap.String("title", req.Title),
	)

	return jobID, nil
}

// GetJobStatus restituisce lo stato di un job
func (s *AsyncPipelineService) GetJobStatus(jobID string) (*PipelineJob, error) {
	s.mu.RLock()
	job, exists := s.jobs[jobID]
	s.mu.RUnlock()

	if !exists {
		// Prova a caricare da disco
		job = s.loadJobFromDisk(jobID)
		if job == nil {
			return nil, fmt.Errorf("job %s not found", jobID)
		}
	}

	return job, nil
}

// ListJobs restituisce tutti i job (con filtro opzionale)
func (s *AsyncPipelineService) ListJobs(status string, limit int) ([]PipelineJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var jobs []PipelineJob
	for _, job := range s.jobs {
		if status != "" && job.Status != status {
			continue
		}
		jobs = append(jobs, *job)
	}

	// Ordina per created_at (più recenti prima)
	// Per semplicità, prendiamo solo gli ultimi N
	if limit > 0 && len(jobs) > limit {
		jobs = jobs[:limit]
	}

	return jobs, nil
}

// CancelJob cancella un job in esecuzione
func (s *AsyncPipelineService) CancelJob(jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, exists := s.jobs[jobID]
	if !exists {
		return fmt.Errorf("job %s not found", jobID)
	}

	if job.Status != "pending" && job.Status != "running" {
		return fmt.Errorf("job %s is %s, cannot cancel", jobID, job.Status)
	}

	// Cancella contesto se in esecuzione
	if cancel, ok := s.cancelFuncs[jobID]; ok {
		cancel()
		delete(s.cancelFuncs, jobID)
	}

	job.Status = "cancelled"
	job.CompletedAt = ptrTime(time.Now())
	job.CurrentStep = "cancelled"

	s.saveJob(job)

	logger.Info("Pipeline job cancelled", zap.String("job_id", jobID))
	return nil
}

// CleanupJobs rimuove job completati/più vecchi di X ore
func (s *AsyncPipelineService) CleanupJobs(maxAge time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	removed := 0

	for id, job := range s.jobs {
		if job.Status == "completed" || job.Status == "failed" || job.Status == "cancelled" {
			if job.CompletedAt != nil && job.CompletedAt.Before(cutoff) {
				delete(s.jobs, id)
				// Rimuovi da disco
				os.Remove(filepath.Join(s.jobsDir, id+".json"))
				removed++
			}
		}
	}

	if removed > 0 {
		logger.Info("Cleaned up old pipeline jobs", zap.Int("removed", removed))
	}

	return removed
}

// executePipeline esegue la pipeline in background
func (s *AsyncPipelineService) executePipeline(jobID string, req *scriptclips.ScriptClipsRequest) {
	s.mu.Lock()
	job := s.jobs[jobID]
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	s.cancelFuncs[jobID] = cancel
	s.mu.Unlock()

	// Aggiorna stato
	s.updateJob(jobID, func(j *PipelineJob) {
		j.Status = "running"
		j.StartedAt = ptrTime(time.Now())
		j.CurrentStep = "script_generation"
		j.Progress = 0
	})

	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.cancelFuncs, jobID)
		s.mu.Unlock()
	}()

	// Create progress callback that updates the job
	progressCallback := func(step string, progress int, message string, entityName string, clipsDone int, clipsTotal int) {
		s.updateJob(jobID, func(j *PipelineJob) {
			j.CurrentStep = step
			j.Progress = progress
			if entityName != "" {
				j.CurrentEntity = entityName
			}
			if clipsTotal > 0 {
				j.TotalClips = clipsTotal
				j.ClipsFound = clipsDone
				j.ClipsMissing = clipsTotal - clipsDone
			}
		})

		logger.Info("Pipeline progress update",
			zap.String("job_id", jobID),
			zap.String("step", step),
			zap.Int("progress", progress),
			zap.String("message", message),
			zap.String("entity", entityName),
			zap.Int("clips_done", clipsDone),
			zap.Int("clips_total", clipsTotal),
		)
	}

	// Add progress callback to request
	req.ProgressCallback = progressCallback

	// Esegui pipeline con callback per progress
	result, err := s.service.GenerateScriptWithClips(ctx, req)

	s.mu.Lock()
	defer s.mu.Unlock()

	job = s.jobs[jobID]
	if err != nil {
		job.Status = "failed"
		job.Error = err.Error()
		job.CompletedAt = ptrTime(time.Now())
		job.CurrentStep = "failed"
		job.Progress = 0
		logger.Error("Pipeline job failed",
			zap.String("job_id", jobID),
			zap.Error(err),
		)
	} else {
		job.Status = "completed"
		job.CompletedAt = ptrTime(time.Now())
		job.CurrentStep = "completed"
		job.Progress = 100
		job.Result = result
		job.ClipsFound = result.TotalClipsFound
		job.ClipsMissing = result.TotalClipsMissing
		logger.Info("Pipeline job completed",
			zap.String("job_id", jobID),
			zap.Int("clips_found", result.TotalClipsFound),
			zap.Int("clips_missing", result.TotalClipsMissing),
			zap.Float64("time_seconds", result.ProcessingTime),
		)
	}

	s.saveJob(job)
}

// updateJob aggiorna un job con una funzione di modifica
func (s *AsyncPipelineService) updateJob(jobID string, updateFn func(*PipelineJob)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, exists := s.jobs[jobID]
	if !exists {
		return
	}

	updateFn(job)
	s.saveJob(job)
}

// saveJob salva un job su disco
func (s *AsyncPipelineService) saveJob(job *PipelineJob) {
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		logger.Warn("Failed to marshal pipeline job", zap.Error(err))
		return
	}

	path := filepath.Join(s.jobsDir, job.ID+".json")
	os.WriteFile(path, data, 0644)
}

// loadJobFromDisk carica un job da disco
func (s *AsyncPipelineService) loadJobFromDisk(jobID string) *PipelineJob {
	path := filepath.Join(s.jobsDir, jobID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var job PipelineJob
	if err := json.Unmarshal(data, &job); err != nil {
		return nil
	}

	return &job
}

// loadJobs carica tutti i job da disco
func (s *AsyncPipelineService) loadJobs() {
	entries, err := os.ReadDir(s.jobsDir)
	if err != nil {
		logger.Warn("Failed to read pipeline jobs directory", zap.Error(err))
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if len(entry.Name()) < 6 || entry.Name()[len(entry.Name())-5:] != ".json" {
			continue
		}

		jobID := entry.Name()[:len(entry.Name())-5]
		job := s.loadJobFromDisk(jobID)
		if job != nil {
			// Se era in esecuzione, mark as failed (server riavviato)
			if job.Status == "running" {
				job.Status = "failed"
				job.Error = "Server restarted during execution"
				s.saveJob(job)
			}
			s.jobs[jobID] = job
		}
	}

	logger.Info("Loaded pipeline jobs from disk",
		zap.Int("jobs", len(s.jobs)),
	)
}

// ptrTime restituisce un puntatore a time.Time
func ptrTime(t time.Time) *time.Time {
	return &t
}
