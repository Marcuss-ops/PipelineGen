package artlist

import (
	"context"
	"database/sql"
	"time"

	"velox/go-master/pkg/models"
)

// JobAdapter provides conversion between artlistRunRecord and models.Job
type JobAdapter struct {
	db *sql.DB
}

// NewJobAdapter creates a new JobAdapter
func NewJobAdapter(db *sql.DB) *JobAdapter {
	return &JobAdapter{db: db}
}

// RunRecordToJob converts an artlistRunRecord to a models.Job
func RunRecordToJob(rec *artlistRunRecord) *models.Job {
	job := models.NewJob(models.JobTypeStockClip, map[string]interface{}{
		"term":            rec.Term,
		"root_folder_id":  rec.RootFolderID,
		"strategy":        rec.Strategy,
		"dry_run":         rec.DryRun,
		"active_key":      rec.ActiveKey,
	})

	job.ID = rec.RunID
	job.Status = models.JobStatus(rec.Status)
	job.Error = rec.Error

	if rec.StartedAt != nil {
		t, _ := time.Parse(time.RFC3339, *rec.StartedAt)
		job.StartedAt = &t
	}

	result := map[string]interface{}{
		"found":          rec.Found,
		"processed":      rec.Processed,
		"skipped":        rec.Skipped,
		"failed":         rec.Failed,
		"estimated_size": rec.EstimatedSize,
		"tag_folder_id":  rec.TagFolderID,
	}
	if rec.LastProcessedAt != nil {
		result["last_processed_at"] = *rec.LastProcessedAt
	}
	job.Result = result

	return job
}

// JobToRunRecord converts a models.Job to an artlistRunRecord
func JobToRunRecord(job *models.Job) *artlistRunRecord {
	rec := &artlistRunRecord{
		RunID:  job.ID,
		Status: string(job.Status),
		Error:  job.Error,
	}

	if job.Payload != nil {
		if v, ok := job.Payload["term"].(string); ok {
			rec.Term = v
		}
		if v, ok := job.Payload["root_folder_id"].(string); ok {
			rec.RootFolderID = v
		}
		if v, ok := job.Payload["strategy"].(string); ok {
			rec.Strategy = v
		}
		if v, ok := job.Payload["dry_run"].(bool); ok {
			rec.DryRun = v
		}
		if v, ok := job.Payload["active_key"].(string); ok {
			rec.ActiveKey = v
		}
	}

	if job.StartedAt != nil {
		t := job.StartedAt.Format(time.RFC3339)
		rec.StartedAt = &t
	}

	if job.Result != nil {
		if v, ok := job.Result["found"].(int); ok {
			rec.Found = v
		}
		if v, ok := job.Result["processed"].(int); ok {
			rec.Processed = v
		}
		if v, ok := job.Result["skipped"].(int); ok {
			rec.Skipped = v
		}
		if v, ok := job.Result["failed"].(int); ok {
			rec.Failed = v
		}
		if v, ok := job.Result["estimated_size"].(int); ok {
			rec.EstimatedSize = v
		}
		if v, ok := job.Result["tag_folder_id"].(string); ok {
			rec.TagFolderID = v
		}
		if v, ok := job.Result["last_processed_at"].(string); ok {
			rec.LastProcessedAt = &v
		}
	}

	return rec
}

// CreateJobRun creates a new job-based run record
func (s *Service) CreateJobRun(ctx context.Context, req *RunTagRequest) (*models.Job, error) {
	job := models.NewJob(models.JobTypeStockClip, map[string]interface{}{
		"term":           req.Term,
		"root_folder_id": req.RootFolderID,
		"strategy":       req.Strategy,
		"dry_run":        req.DryRun,
		"active_key":     runDedupKey(req.Term, req.RootFolderID, req.Strategy, req.DryRun),
	})

	job.Status = models.StatusRunning
	now := time.Now()
	job.StartedAt = &now

	return job, nil
}

// UpdateJobRun updates an existing job with run results
func (s *Service) UpdateJobRun(ctx context.Context, job *models.Job, resp *RunTagResponse) error {
	job.Status = models.JobStatus(resp.Status)
	job.Error = resp.Error

	if resp.Found > 0 || resp.Processed > 0 || resp.Skipped > 0 || resp.Failed > 0 {
		job.Result = map[string]interface{}{
			"found":          resp.Found,
			"processed":      resp.Processed,
			"skipped":        resp.Skipped,
			"failed":         resp.Failed,
			"estimated_size": resp.EstimatedSize,
			"tag_folder_id":  resp.TagFolderID,
		}
		if resp.LastProcessedAt != nil {
			job.Result["last_processed_at"] = *resp.LastProcessedAt
		}
	}

	return nil
}

// GetJobByRunID retrieves a job by its run ID (which is the job ID)
func (s *Service) GetJobByRunID(ctx context.Context, runID string) (*models.Job, error) {
	rec, err := s.loadRunRecord(ctx, runID)
	if err != nil {
		return nil, err
	}
	return RunRecordToJob(rec), nil
}

// FindActiveJob finds an active job by its active key
func (s *Service) FindActiveJob(ctx context.Context, activeKey string) (*models.Job, error) {
	rec, err := s.findActiveRunRecord(ctx, activeKey)
	if err != nil {
		return nil, err
	}
	return RunRecordToJob(rec), nil
}

// CanRetryRun checks if a run can be retried using the Job model's CanRetry logic
func (s *Service) CanRetryRun(runID string) bool {
	// This would need to load the job first
	// For now, return false as the original implementation doesn't have retry
	return false
}
