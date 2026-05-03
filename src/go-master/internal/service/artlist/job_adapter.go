package artlist

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
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
	payload := models.ArtlistRunPayload{
		Term:         rec.Term,
		RootFolderID: rec.RootFolderID,
		Strategy:     rec.Strategy,
		DryRun:       rec.DryRun,
	}
	payloadBytes, _ := json.Marshal(payload)
	
	job := models.NewJob(models.JobTypeArtlistRun, payloadBytes)
	job.ActiveKey = rec.ActiveKey

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

	if len(job.Payload) > 0 {
		var payload models.ArtlistRunPayload
		if err := json.Unmarshal(job.Payload, &payload); err == nil {
			rec.Term = payload.Term
			rec.RootFolderID = payload.RootFolderID
			rec.Strategy = payload.Strategy
			rec.DryRun = payload.DryRun
		}
	}
	if job.ActiveKey != "" {
		rec.ActiveKey = job.ActiveKey
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
	rec, _, err := s.ensureRunRecord(ctx, req)
	if err != nil {
		return nil, err
	}
	return RunRecordToJob(rec), nil
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
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, fmt.Errorf("run_id is required")
	}

	// New job-system IDs should be read directly from jobs.db.sqlite.
	if strings.HasPrefix(runID, "job_") {
		if s.jobsDB == nil {
			return nil, fmt.Errorf("jobs database not configured")
		}
		job, err := s.loadJobFromJobsDB(ctx, runID)
		if err == nil {
			return job, nil
		}
		return nil, err
	}

	// Legacy artlist_runs UUID path.
	rec, err := s.loadRunRecord(ctx, runID)
	if err == nil {
		return RunRecordToJob(rec), nil
	}

	// Final fallback: maybe caller passed a non-job ID that still exists in jobs DB.
	if s.jobsDB != nil {
		if job, err2 := s.loadJobFromJobsDB(ctx, runID); err2 == nil {
			return job, nil
		}
	}

	return nil, err
}

// loadJobFromJobsDB loads a job directly from the jobs database
func (s *Service) loadJobFromJobsDB(ctx context.Context, jobID string) (*models.Job, error) {
	var job models.Job
	var payloadJSON string
	var resultJSON string
	var leaseExpiry, createdAt, updatedAt, startedAt, completedAt, cancelledAt *string

	query := `
		SELECT id, type, status, priority, project, video_name, active_key,
		       payload_json, result_json, progress, error, retry_count, max_retries,
		       worker_id, lease_expiry, created_at, updated_at, started_at, completed_at, cancelled_at
		FROM jobs
		WHERE id = ?
		LIMIT 1
	`

	err := s.jobsDB.QueryRowContext(ctx, query, jobID).Scan(
		&job.ID,
		&job.Type,
		&job.Status,
		&job.Priority,
		&job.Project,
		&job.VideoName,
		&job.ActiveKey,
		&payloadJSON,
		&resultJSON,
		&job.Progress,
		&job.Error,
		&job.RetryCount,
		&job.MaxRetries,
		&job.WorkerID,
		&leaseExpiry,
		&createdAt,
		&updatedAt,
		&startedAt,
		&completedAt,
		&cancelledAt,
	)
	if err != nil {
		return nil, err
	}

	_ = json.Unmarshal([]byte(payloadJSON), &job.Payload)
	_ = json.Unmarshal([]byte(resultJSON), &job.Result)

	job.LeaseExpiry = parseTimePtr(leaseExpiry)
	job.CreatedAt = parseTimeValue(createdAt)
	job.UpdatedAt = parseTimeValue(updatedAt)
	job.StartedAt = parseTimePtr(startedAt)
	job.CompletedAt = parseTimePtr(completedAt)
	job.CancelledAt = parseTimePtr(cancelledAt)

	return &job, nil
}

func parseTimePtr(v *string) *time.Time {
	if v == nil || strings.TrimSpace(*v) == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(*v))
	if err != nil {
		return nil
	}
	return &t
}

func parseTimeValue(v *string) time.Time {
	if parsed := parseTimePtr(v); parsed != nil {
		return *parsed
	}
	return time.Time{}
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
