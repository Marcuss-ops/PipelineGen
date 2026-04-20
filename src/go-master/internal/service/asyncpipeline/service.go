package asyncpipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/service/scriptclips"
	"velox/go-master/pkg/logger"
)

// NewAsyncPipelineService crea un nuovo servizio.
func NewAsyncPipelineService(service *scriptclips.ScriptClipsService, dataDir string) *AsyncPipelineService {
	jobsDir := filepath.Join(dataDir, "async_pipeline_jobs")
	_ = os.MkdirAll(jobsDir, 0o755)

	svc := &AsyncPipelineService{
		service:     service,
		jobsDir:     jobsDir,
		jobs:        make(map[string]*PipelineJob),
		cancelFuncs: make(map[string]context.CancelFunc),
	}

	// Load existing jobs so polling endpoints keep working after restart.
	svc.loadJobs()
	return svc
}

// StartPipeline avvia una pipeline asincrona e restituisce il job ID.
func (s *AsyncPipelineService) StartPipeline(req *scriptclips.ScriptClipsRequest) (string, error) {
	jobID := fmt.Sprintf("pipeline_%d", time.Now().UnixNano())
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

	s.saveJob(job)
	go s.executePipeline(jobID, req)

	logger.Info("Pipeline job started",
		zap.String("job_id", jobID),
		zap.String("title", req.Title),
	)
	return jobID, nil
}

// GetJobStatus restituisce lo stato di un job.
func (s *AsyncPipelineService) GetJobStatus(jobID string) (*PipelineJob, error) {
	s.mu.RLock()
	job, exists := s.jobs[jobID]
	s.mu.RUnlock()
	if exists {
		return job, nil
	}

	job = s.loadJobFromDisk(jobID)
	if job == nil {
		return nil, fmt.Errorf("job %s not found", jobID)
	}
	return job, nil
}

// ListJobs restituisce tutti i job con filtro opzionale.
func (s *AsyncPipelineService) ListJobs(status string, limit int) ([]PipelineJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]PipelineJob, 0, len(s.jobs))
	for _, job := range s.jobs {
		if status != "" && job.Status != status {
			continue
		}
		out = append(out, *job)
	}

	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// CancelJob cancella un job pending/running.
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

// CleanupJobs rimuove job terminali più vecchi di maxAge.
func (s *AsyncPipelineService) CleanupJobs(maxAge time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for id, job := range s.jobs {
		if job.Status != "completed" && job.Status != "failed" && job.Status != "cancelled" {
			continue
		}
		if job.CompletedAt == nil || !job.CompletedAt.Before(cutoff) {
			continue
		}
		delete(s.jobs, id)
		_ = os.Remove(filepath.Join(s.jobsDir, id+".json"))
		removed++
	}

	if removed > 0 {
		logger.Info("Cleaned up old pipeline jobs", zap.Int("removed", removed))
	}
	return removed
}
