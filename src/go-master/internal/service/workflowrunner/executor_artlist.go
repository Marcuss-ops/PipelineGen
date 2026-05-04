package workflowrunner

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/service/artlist"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/pkg/models"
)

// artlistExecutor executes artlist.run workflow steps via the common jobs service
type artlistExecutor struct {
	jobsSvc *jobservice.Service
	log     *zap.Logger
}

// newArtlistExecutor creates a new artlist step executor
// Deprecated: Use newArtlistExecutorV2 with jobs service instead
func newArtlistExecutor(svc *artlist.Service, log *zap.Logger) StepExecutor {
	return &artlistExecutor{
		log: log.With(zap.String("executor", "artlist.run")),
	}
}

// newArtlistExecutorV2 creates a new artlist step executor using the jobs service
func newArtlistExecutorV2(jobsSvc *jobservice.Service, log *zap.Logger) StepExecutor {
	return &artlistExecutor{
		jobsSvc: jobsSvc,
		log:     log.With(zap.String("executor", "artlist.run")),
	}
}

// Execute runs the artlist step via the common jobs service
func (e *artlistExecutor) Execute(ctx context.Context, input *StepInput) (*StepOutput, error) {
	if e.jobsSvc == nil {
		return nil, fmt.Errorf("jobs service not configured for artlist executor")
	}

	// Parse step parameters from Payload (rendered from With by runner)
	term, _ := input.Payload["term"].(string)
	limit := 0
	if l, ok := input.Payload["limit"].(int); ok {
		limit = l
	} else if l, ok := input.Payload["limit"].(float64); ok {
		limit = int(l)
	}
	strategy, _ := input.Payload["strategy"].(string)
	dryRun, _ := input.Payload["dry_run"].(bool)

	if term == "" {
		return nil, fmt.Errorf("artlist.run: term is required")
	}

	req := &artlist.RunTagRequest{
		Term:     term,
		Limit:    limit,
		Strategy: strategy,
		DryRun:   dryRun,
	}

	// Enqueue through common jobs service
	job, err := e.jobsSvc.Enqueue(ctx, &jobservice.EnqueueRequest{
		Type:       models.JobTypeArtlistRun,
		Payload:    req.ToMap(),
		MaxRetries: 3,
		ActiveKey:  artlist.RunDedupKey(req.Term, req.RootFolderID, req.Strategy, req.DryRun),
	})
	if err != nil {
		return &StepOutput{
			OK:     false,
			Status: "failed",
			Error:  fmt.Sprintf("failed to enqueue artlist job: %v", err),
		}, nil
	}

	// If no wait requested, return immediately with run ID
	if input.Step.Wait == nil {
		return &StepOutput{
			OK:     true,
			Status: "queued",
			RunID:  job.ID,
		}, nil
	}

	// Wait for job completion
	finalJob, err := e.waitForJob(ctx, job.ID, input.Step.Wait)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for artlist run: %w", err)
	}

	return e.jobToStepOutput(finalJob), nil
}

// waitForJob polls the jobs service until terminal status
func (e *artlistExecutor) waitForJob(ctx context.Context, jobID string, waitCfg *WaitConfig) (*models.Job, error) {
	timeout := 900 // default 15 minutes
	if waitCfg.TimeoutSeconds > 0 {
		timeout = waitCfg.TimeoutSeconds
	}
	interval := 2000 // default 2 seconds
	if waitCfg.IntervalMS > 0 {
		interval = waitCfg.IntervalMS
	}

	timeoutDuration := time.Duration(timeout) * time.Second
	intervalDuration := time.Duration(interval) * time.Millisecond
	deadline := time.Now().Add(timeoutDuration)

	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for artlist job %s", jobID)
		}

		job, err := e.jobsSvc.Get(ctx, jobID)
		if err != nil {
			e.log.Warn("failed to get job status", zap.String("job_id", jobID), zap.Error(err))
			// Wait for interval or context cancellation
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("context cancelled while waiting for artlist job %s: %w", jobID, ctx.Err())
			case <-time.After(intervalDuration):
				// Continue to next status check
			}
			continue
		}

		// Check terminal status
		if job.Status.IsTerminal() {
			if job.Status == models.StatusFailed {
				return job, fmt.Errorf("artlist job %s failed: %s", jobID, job.Error)
			}
			return job, nil
		}

		// Wait before next poll
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled while waiting for artlist job %s: %w", jobID, ctx.Err())
		case <-time.After(intervalDuration):
			// Continue
		}
	}
}

// jobToStepOutput converts a models.Job to standardized StepOutput
func (e *artlistExecutor) jobToStepOutput(job *models.Job) *StepOutput {
	output := &StepOutput{
		OK:     job.Status == models.StatusCompleted,
		Status: string(job.Status),
		RunID:  job.ID,
		Error:  job.Error,
	}

	// Extract items from job result if available
	if job.Result != nil {
		if itemsRaw, ok := job.Result["items"].([]interface{}); ok {
			for _, itemRaw := range itemsRaw {
				if itemMap, ok := itemRaw.(map[string]interface{}); ok {
					item := AssetItem{
						Source: "artlist",
					}
					if v, ok := itemMap["clip_id"].(string); ok {
						item.ID = v
					}
					if v, ok := itemMap["name"].(string); ok {
						item.Title = v
					}
					if v, ok := itemMap["drive_link"].(string); ok {
						item.DriveLink = v
					}
					if v, ok := itemMap["local_path"].(string); ok {
						item.LocalPath = v
					}
					if v, ok := itemMap["file_hash"].(string); ok {
						item.Hash = v
					}
					if v, ok := itemMap["status"].(string); ok {
						item.Status = v
					}
					if v, ok := itemMap["error"].(string); ok {
						item.Error = v
					}
					output.Items = append(output.Items, item)
				}
			}
		}

		// Add summary data from result
		if v, ok := job.Result["tag_folder_id"].(string); ok {
			output.FolderID = v
		}
		output.Raw = job.Result
	}

	return output
}
