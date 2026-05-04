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

// CreateJobRun creates a new job and enqueues it in the jobs table.
// Note: artlist_runs is legacy read-only. New runs use jobs table directly.
func (s *Service) CreateJobRun(ctx context.Context, req *RunTagRequest) (*models.Job, error) {
	if s.jobsDB == nil {
		return nil, fmt.Errorf("jobs database not configured")
	}

	// Build payload
	payload := models.ArtlistRunPayload{
		Term:         req.Term,
		RootFolderID: req.RootFolderID,
		Strategy:     req.Strategy,
		DryRun:       req.DryRun,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	job := models.NewJob(models.JobTypeArtlistRun, payloadBytes)
	job.ActiveKey = runDedupKey(req.Term, req.RootFolderID, req.Strategy, req.DryRun)

	// Insert into jobs table
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.jobsDB.ExecContext(ctx, `
		INSERT INTO jobs (id, "type", status, priority, project, video_name, active_key,
		   payload_json, result_json, progress, "error", retry_count, max_retries,
		   created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, job.ID, job.Type, job.Status, job.Priority, job.Project, job.VideoName, job.ActiveKey,
		string(payloadBytes), "{}", 0, "", 0, job.MaxRetries,
		now, now)
	if err != nil {
		return nil, fmt.Errorf("failed to enqueue job: %w", err)
	}

	return job, nil
}

// jobToRunTagResponse converts a job to a RunTagResponse.
func jobToRunTagResponse(job *models.Job) *RunTagResponse {
	resp := &RunTagResponse{
		OK:     true,
		RunID:  job.ID,
		Status: string(job.Status),
		Term:   job.Project, // project field stores the term
		DryRun: strings.HasSuffix(job.ActiveKey, "true"),
	}
	// Extract additional fields from payload
	if len(job.Payload) > 0 {
		var payload models.ArtlistRunPayload
		if err := json.Unmarshal(job.Payload, &payload); err == nil {
			resp.Strategy = payload.Strategy
			resp.RootFolderID = payload.RootFolderID
		}
	}
	return resp
}

func (s *Service) persistJob(ctx context.Context, job *models.Job) error {
	if s.jobsDB == nil {
		return fmt.Errorf("jobs database not configured")
	}
	// Update job directly in jobs table
	_, err := s.jobsDB.ExecContext(ctx, `
		UPDATE jobs
		SET status = ?,
			error = ?,
			updated_at = ?
		WHERE id = ?
	`, job.Status, job.Error, time.Now().UTC().Format(time.RFC3339), job.ID)
	return err
}

func unmarshalPayload(payload json.RawMessage) (map[string]interface{}, error) {
	var result map[string]interface{}
	if len(payload) == 0 {
		return result, nil
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
	}
	return result, nil
}

func (s *Service) StartRunTag(ctx context.Context, req *RunTagRequest) (*RunTagResponse, error) {
	normalized := normalizeRunRequest(req)
	if normalized.Term == "" {
		return &RunTagResponse{OK: false, Error: "term is required"}, fmt.Errorf("term is required")
	}
	if normalized.RootFolderID == "" && s.driveService != nil {
		normalized.RootFolderID = strings.TrimSpace(s.driveService.GetDriveFolderID())
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
			payload, err := unmarshalPayload(existingJob.Payload)
			if err != nil {
				s.log.Warn("failed to unmarshal payload for stale job", zap.String("job_id", existingJob.ID), zap.Error(err))
			}
			if t, ok := payload["term"].(string); ok {
				term = t
			}
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
			if err := s.persistJob(ctx, existingJob); err != nil {
				s.log.Error("failed to persist stale job", zap.String("job_id", existingJob.ID), zap.Error(err))
			}
			// Proceed to create a new job
		} else {
			resp := jobToResponse(existingJob)
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

	resp := jobToResponse(job)
	s.log.Info("artlist run queued",
		zap.String("run_id", resp.RunID),
		zap.String("term", resp.Term),
		zap.String("root_folder_id", resp.RootFolderID),
		zap.String("strategy", resp.Strategy),
		zap.Bool("dry_run", resp.DryRun),
	)

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
			job, err := s.GetJobByRunID(ctx, jobID)
			if err != nil {
				s.log.Error("failed to load job after panic", zap.String("job_id", jobID), zap.Error(err))
				return
			}
			if job != nil {
				job.Status = models.StatusFailed
				job.Error = fmt.Sprintf("internal panic: %v", r)
				if err := s.persistJob(ctx, job); err != nil {
					s.log.Error("failed to persist job after panic", zap.String("job_id", jobID), zap.Error(err))
				}
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

		s.log.Info("calling RunTag", zap.String("job_id", jobID), zap.String("term", req.Term), zap.Bool("ctx_canceled", ctx.Err() != nil))
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

		// During retries, don't clear active_key
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
			if err := s.persistJob(ctx, job); err != nil {
				s.log.Warn("failed to update job record during retry",
					zap.String("job_id", jobID),
					zap.Error(err),
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
	return jobToResponse(job), nil
}

// jobToResponse converts a models.Job to RunTagResponse.
// getIntFromResult extracts an int from a result map, handling both int and float64 types
func getIntFromResult(m map[string]interface{}, key string) int {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	default:
		return 0
	}
}

func jobToResponse(job *models.Job) *RunTagResponse {
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
		resp.Found = getIntFromResult(job.Result, "found")
		resp.Processed = getIntFromResult(job.Result, "processed")
		resp.Skipped = getIntFromResult(job.Result, "skipped")
		resp.Failed = getIntFromResult(job.Result, "failed")
		resp.EstimatedSize = getIntFromResult(job.Result, "estimated_size")
		if v, ok := job.Result["tag_folder_id"].(string); ok {
			resp.TagFolderID = v
		}
		if v, ok := job.Result["last_processed_at"].(string); ok {
			resp.LastProcessedAt = &v
		}
		// Extract items from result
		if itemsRaw, ok := job.Result["items"].([]interface{}); ok {
			for _, itemRaw := range itemsRaw {
				if itemMap, ok := itemRaw.(map[string]interface{}); ok {
					item := RunTagItem{}
					if v, ok := itemMap["clip_id"].(string); ok {
						item.ClipID = v
					}
					if v, ok := itemMap["name"].(string); ok {
						item.Name = v
					}
					if v, ok := itemMap["filename"].(string); ok {
						item.Filename = v
					}
					if v, ok := itemMap["status"].(string); ok {
						item.Status = v
					}
					if v, ok := itemMap["drive_link"].(string); ok {
						item.DriveLink = v
					}
					if v, ok := itemMap["download_link"].(string); ok {
						item.DownloadLink = v
					}
					if v, ok := itemMap["local_path"].(string); ok {
						item.LocalPath = v
					}
					if v, ok := itemMap["file_hash"].(string); ok {
						item.FileHash = v
					}
					if v, ok := itemMap["error"].(string); ok {
						item.Error = v
					}
					resp.Items = append(resp.Items, item)
				}
			}
		}
	}

	return resp
}

// JobToRunTagResponse converts a models.Job to RunTagResponse.
func JobToRunTagResponse(job *models.Job) *RunTagResponse {
	return jobToResponse(job)
}
