package asyncpipeline

import (
	"context"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/service/scriptclips"
	"velox/go-master/pkg/logger"
)

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
