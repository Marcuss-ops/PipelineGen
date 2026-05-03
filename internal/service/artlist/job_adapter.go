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

// RunRecordToJob converts an artlistRunRecord to a models.Job.
// This is used for reading legacy artlist_runs records.
// Returns error if JSON marshaling fails.
func RunRecordToJob(rec *artlistRunRecord) (*models.Job, error) {
	payload := models.ArtlistRunPayload{
		Term:         rec.Term,
		RootFolderID: rec.RootFolderID,
		Strategy:     rec.Strategy,
		DryRun:       rec.DryRun,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	job := models.NewJob(models.JobTypeArtlistRun, payloadBytes)
	job.ActiveKey = rec.ActiveKey

	job.ID = rec.RunID
	job.Status = models.JobStatus(rec.Status)
	job.Error = rec.Error

	if rec.StartedAt != nil {
		job.StartedAt = rec.StartedAt
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
		result["last_processed_at"] = rec.LastProcessedAt.Format(time.RFC3339)
	}
	job.Result = result

	return job, nil
}

// UpdateJobRun updates an existing job with run results (in memory).
// The caller is responsible for persisting the job.
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

		// Include items with detailed status
		if len(resp.Items) > 0 {
			items := make([]map[string]interface{}, 0, len(resp.Items))
			for _, item := range resp.Items {
				items = append(items, map[string]interface{}{
					"clip_id":       item.ClipID,
					"name":          item.Name,
					"filename":      item.Filename,
					"status":        item.Status,
					"drive_link":    item.DriveLink,
					"download_link": item.DownloadLink,
					"local_path":    item.LocalPath,
					"file_hash":     item.FileHash,
					"error":         item.Error,
				})
			}
			job.Result["items"] = items
		}
	}

	return nil
}

// GetJobByRunID retrieves a job by its run ID (which is the job ID).
// Jobs table is the source of truth. Legacy artlist_runs is read-only for old records.
func (s *Service) GetJobByRunID(ctx context.Context, runID string) (*models.Job, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, fmt.Errorf("run_id is required")
	}

	// Jobs table is the source of truth
	if s.jobsDB == nil {
		return nil, fmt.Errorf("jobs database not configured")
	}
	job, err := s.loadJobFromJobsDB(ctx, runID)
	if err == nil {
		return job, nil
	}

	// Fallback: try legacy artlist_runs for backward compatibility with old UUIDs
	rec, err2 := s.loadRunRecord(ctx, runID)
	if err2 == nil {
		return RunRecordToJob(rec)
	}

	return nil, err
}

// loadJobFromJobsDB loads a job directly from the jobs database.
func (s *Service) loadJobFromJobsDB(ctx context.Context, jobID string) (*models.Job, error) {
	var job models.Job
	var payloadJSON string
	var resultJSON string
	var leaseExpiry, createdAt, updatedAt, startedAt, completedAt, cancelledAt *string

	query := `
	SELECT id, "type", status, priority, project, video_name, active_key,
		   payload_json, result_json, progress, "error", retry_count, max_retries,
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

	if err := json.Unmarshal([]byte(payloadJSON), &job.Payload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal job payload: %w", err)
	}
	if err := json.Unmarshal([]byte(resultJSON), &job.Result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal job result: %w", err)
	}

	job.LeaseExpiry = parseTimePtr(leaseExpiry)
	job.CreatedAt = parseTimeValue(createdAt)
	job.UpdatedAt = parseTimeValue(updatedAt)
	job.StartedAt = parseTimePtr(startedAt)
	job.CompletedAt = parseTimePtr(completedAt)
	job.CancelledAt = parseTimePtr(cancelledAt)

	return &job, nil
}

// FindActiveJob finds an active job by its active key.
// Jobs table is the source of truth. Legacy artlist_runs is read-only.
func (s *Service) FindActiveJob(ctx context.Context, activeKey string) (*models.Job, error) {
	if s.jobsDB == nil {
		return nil, fmt.Errorf("jobs database not configured")
	}
	job, err := s.findActiveJobInJobsDB(ctx, activeKey)
	if err == nil && job != nil && !job.Status.IsTerminal() {
		return job, nil
	}
	// Fallback to legacy for backward compatibility
	rec, err2 := s.findActiveRunRecord(ctx, activeKey)
	if err2 == nil && rec != nil {
		return RunRecordToJob(rec)
	}
	return nil, err
}

// findActiveJobInJobsDB finds an active job in the jobs table by active_key.
func (s *Service) findActiveJobInJobsDB(ctx context.Context, activeKey string) (*models.Job, error) {
	query := `
	SELECT id, "type", status, priority, project, video_name, active_key,
		   payload_json, result_json, progress, "error", retry_count, max_retries,
		   worker_id, lease_expiry, created_at, updated_at, started_at, completed_at, cancelled_at
	FROM jobs
	WHERE active_key = ? AND status IN ('queued', 'running')
	ORDER BY started_at DESC
	LIMIT 1
	`

	var job models.Job
	var payloadJSON string
	var resultJSON string
	var leaseExpiry, createdAt, updatedAt, startedAt, completedAt, cancelledAt *string

	err := s.jobsDB.QueryRowContext(ctx, query, activeKey).Scan(
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

	if err := json.Unmarshal([]byte(payloadJSON), &job.Payload); err != nil {
		s.log.Error("failed to unmarshal job payload", zap.String("job_id", job.ID), zap.Error(err))
	}
	if err := json.Unmarshal([]byte(resultJSON), &job.Result); err != nil {
		s.log.Error("failed to unmarshal job result", zap.String("job_id", job.ID), zap.Error(err))
	}

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
