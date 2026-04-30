package artlist

import (
	"context"
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
		resp := s.jobToResponse(existingJob)
		s.log.Info("artlist run reused",
			zap.String("run_id", resp.RunID),
			zap.String("term", resp.Term),
			zap.String("status", resp.Status),
		)
		return resp, nil
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

	go s.executeRunTag(context.Background(), normalized, job.ID)
	return resp, nil
}

func (s *Service) executeRunTag(ctx context.Context, req *RunTagRequest, jobID string) {
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
			job.Retries = attempt + 1
			job.Error = resp.Error
		}

		if err := s.persistJob(ctx, job); err != nil {
			s.log.Warn("failed to update job record",
				zap.String("job_id", jobID),
				zap.Error(err),
			)
		}

		if job.CanRetry() && status == models.StatusFailed {
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

	if job.Payload != nil {
		if v, ok := job.Payload["term"].(string); ok {
			resp.Term = v
		}
		if v, ok := job.Payload["strategy"].(string); ok {
			resp.Strategy = v
		}
		resp.DryRun = job.Payload["dry_run"] == true
		if v, ok := job.Payload["root_folder_id"].(string); ok {
			resp.RootFolderID = v
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
