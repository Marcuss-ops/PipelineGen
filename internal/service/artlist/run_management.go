package artlist

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/pkg/models"
)

func (s *Service) persistJob(ctx context.Context, job *models.Job) error {
	rec := JobToRunRecord(job)
	status := string(job.Status)

	return s.finishRunRecord(ctx, rec.RunID, status, s.runRecordToResponse(rec))
}

func unmarshalPayload(payload json.RawMessage) map[string]interface{} {
	var result map[string]interface{}
	if len(payload) > 0 {
		json.Unmarshal(payload, &result)
	}
	return result
}

func (s *Service) StartRunTag(ctx context.Context, req *RunTagRequest) (*RunTagResponse, error) {
	normalized := normalizeRunRequest(req)
	if normalized.Term == "" {
		return &RunTagResponse{OK: false, Error: "term is required"}, fmt.Errorf("term is required")
	}
	if normalized.RootFolderID == "" {
		normalized.RootFolderID = strings.TrimSpace(s.driveFolderID)
	}
	if normalized.RootFolderID == "" {
		normalized.RootFolderID = "root"
	}

	activeKey := runDedupKey(normalized.Term, normalized.RootFolderID, normalized.Strategy, normalized.DryRun)
	existingJob, err := s.FindActiveJob(ctx, activeKey)
	if err == nil && existingJob != nil && !existingJob.Status.IsTerminal() {
		// Anti-zombie logic: if job is running/queued for more than 15 minutes, mark it failed
		isStale := false
		if existingJob.StartedAt != nil && time.Since(*existingJob.StartedAt) > 15*time.Minute {
			isStale = true
		} else if time.Since(existingJob.CreatedAt) > 20*time.Minute {
			isStale = true
		}

		if isStale {
			// Get term from payload
			term := ""
			payload := unmarshalPayload(existingJob.Payload)
			if t, ok := payload["term"].(string); ok {
				term = t
			}
			s.log.Warn("marking stale artlist job as failed",
				zap.String("job_id", existingJob.ID),
				zap.String("term", term),
				zap.Time("created_at", existingJob.CreatedAt),
			)
			existingJob.Status = models.StatusFailed
			existingJob.Error = "stale job timeout (zombie)"
			_ = s.persistJob(ctx, existingJob)
			// Proceed to create a new job
		} else {
			resp := s.jobToResponse(existingJob)
			s.log.Info("artlist run reused",
				zap.String("run_id", resp.RunID),
				zap.String("term", resp.Term),
				zap.String("status", resp.Status),
			)
			return resp, nil
		}
	}

	job, err := s.CreateJobRun(ctx, normalized)
	if err != nil {
		return &RunTagResponse{OK: false, Error: err.Error()}, err
	}

	resp := s.jobToResponse(job)
	s.log.Info("artlist run queued",
		zap.String("run_id", resp.RunID),
		zap.String("term", resp.Term),
		zap.String("root_folder_id", resp.RootFolderID),
		zap.String("strategy", resp.Strategy),
		zap.Bool("dry_run", resp.DryRun),
	)

	go func() {
		jobCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		s.executeRunTag(jobCtx, normalized, job.ID)
	}()
	return resp, nil
}

func (s *Service) executeRunTag(ctx context.Context, req *RunTagRequest, jobID string) {
	s.log.Info("executeRunTag started", zap.String("job_id", jobID), zap.String("term", req.Term), zap.Bool("ctx_canceled", ctx.Err() != nil))
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("FATAL PANIC in executeRunTag",
				zap.Any("panic", r),
				zap.String("job_id", jobID),
				zap.String("term", req.Term),
			)
			// Update job status to failed on panic - clear active_key on fatal error
			job, _ := s.GetJobByRunID(ctx, jobID)
			if job != nil {
				job.Status = models.StatusFailed
				job.Error = fmt.Sprintf("internal panic: %v", r)
				_ = s.persistJob(ctx, job)
			}
		}
	}()

	maxRetries := 3
	retryDelay := time.Second * 5

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			s.log.Info("retrying artlist run",
				zap.String("job_id", jobID),
				zap.Int("attempt", attempt),
				zap.Int("max_retries", maxRetries),
			)
			time.Sleep(retryDelay)
		}

		resp, err := s.RunTag(ctx, req)
		status := models.StatusCompleted
		if err != nil || (resp != nil && !resp.OK) {
			status = models.StatusFailed
		}
		if resp == nil {
			resp = &RunTagResponse{OK: false, Term: strings.TrimSpace(req.Term)}
		}
		if status == models.StatusFailed && resp.Error == "" && err != nil {
			resp.Error = err.Error()
		}

		job, jobErr := s.GetJobByRunID(ctx, jobID)
		if jobErr != nil {
			s.log.Warn("failed to load job for update",
				zap.String("job_id", jobID),
				zap.Error(jobErr),
			)
			return
		}

		s.UpdateJobRun(ctx, job, resp)
		job.Status = status
		if status == models.StatusFailed {
			job.RetryCount = attempt + 1
			job.Error = resp.Error
		}

		// During retries, don't clear active_key (use finishRunRecordWithActiveKey with clearActiveKey=false)
		// On final attempt or success, clear active_key
		isLastAttempt := attempt >= maxRetries || status == models.StatusCompleted
		if isLastAttempt {
			// Final state: clear active_key
			if err := s.persistJob(ctx, job); err != nil {
				s.log.Warn("failed to update job record",
					zap.String("job_id", jobID),
					zap.Error(err),
				)
			}
		} else if job.CanRetry() && status == models.StatusFailed {
			// Retry state: preserve active_key
			rec := JobToRunRecord(job)
			if finishErr := s.finishRunRecordWithActiveKey(ctx, rec.RunID, string(job.Status), s.runRecordToResponse(rec), false); finishErr != nil {
				s.log.Warn("failed to update job record during retry",
					zap.String("job_id", jobID),
					zap.Error(finishErr),
				)
			}
			continue
		}
		return
	}
}

func (s *Service) GetRunTag(ctx context.Context, runID string) (*RunTagResponse, error) {
	job, err := s.GetJobByRunID(ctx, runID)
	if err != nil {
		return nil, err
	}
	return s.jobToResponse(job), nil
}

func (s *Service) jobToResponse(job *models.Job) *RunTagResponse {
	if job == nil {
		return &RunTagResponse{OK: false, Status: "not_found", Error: "job not found"}
	}

	resp := &RunTagResponse{
		OK:        job.Status != models.StatusFailed,
		RunID:     job.ID,
		Status:    string(job.Status),
		Error:     job.Error,
		Found:     0,
		Processed: 0,
		Skipped:   0,
		Failed:    0,
		StartedAt: nil,
		EndedAt:   nil,
	}

	if job.StartedAt != nil {
		started := job.StartedAt.Format(time.RFC3339)
		resp.StartedAt = &started
	}
	if job.CompletedAt != nil {
		ended := job.CompletedAt.Format(time.RFC3339)
		resp.EndedAt = &ended
	}

	if job.Payload != nil && len(job.Payload) > 0 {
		var payload map[string]interface{}
		if err := json.Unmarshal(job.Payload, &payload); err == nil {
			if v, ok := payload["term"].(string); ok {
				resp.Term = v
			}
			if v, ok := payload["strategy"].(string); ok {
				resp.Strategy = v
			}
			if v, ok := payload["dry_run"].(bool); ok {
				resp.DryRun = v
			}
			if v, ok := payload["root_folder_id"].(string); ok {
				resp.RootFolderID = v
			}
		}
	}

	if job.Result != nil {
		if v, ok := job.Result["found"].(int); ok {
			resp.Found = v
		}
		if v, ok := job.Result["processed"].(int); ok {
			resp.Processed = v
		}
		if v, ok := job.Result["skipped"].(int); ok {
			resp.Skipped = v
		}
		if v, ok := job.Result["failed"].(int); ok {
			resp.Failed = v
		}
		if v, ok := job.Result["estimated_size"].(int); ok {
			resp.EstimatedSize = v
		}
		if v, ok := job.Result["tag_folder_id"].(string); ok {
			resp.TagFolderID = v
		}
		if v, ok := job.Result["last_processed_at"].(string); ok {
			resp.LastProcessedAt = &v
		}
	}

	return resp
}
