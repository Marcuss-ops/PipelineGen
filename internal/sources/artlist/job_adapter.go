package artlist

import (
	"context"
	"fmt"
	"strings"

	"velox/go-master/internal/media/models"
)

// UpdateJobRun updates an existing job with run results using the codec.
// The caller is responsible for persisting the job.
func (a *JobAdapter) UpdateJobRun(ctx context.Context, job *models.Job, resp *RunTagResponse) error {
	job.Status = models.JobStatus(resp.Status)
	job.Error = resp.Error
	job.Result = jobCodec.ResultFromResponse(resp)
	return nil
}

// GetJobByRunID retrieves a job by its run ID (which is the job ID).
// Jobs table is the ONLY source of truth. Legacy artlist_runs is deprecated and no longer queried.
func (a *JobAdapter) GetJobByRunID(ctx context.Context, runID string) (*models.Job, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, fmt.Errorf("run_id is required")
	}

	job, err := a.service.jobsSvc.Get(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("job not found in jobs table (legacy artlist_runs is deprecated): %w", err)
	}
	return job, nil
}

// FindActiveJob finds an active job by its active key.
// Jobs table is the ONLY source of truth. Legacy artlist_runs is deprecated.
func (a *JobAdapter) FindActiveJob(ctx context.Context, activeKey string) (*models.Job, error) {
	job, err := a.service.jobsSvc.FindActiveByKey(ctx, activeKey)
	if err != nil {
		return nil, err
	}
	if job != nil && !job.Status.IsTerminal() {
		return job, nil
	}
	return nil, nil
}
