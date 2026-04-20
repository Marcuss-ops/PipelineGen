package asyncpipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
)

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
