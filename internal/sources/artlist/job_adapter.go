package artlist

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/job"
)

// GetJobByRunID retrieves a job by its run ID (which is the job ID).
// Artlist job metadata now lives exclusively in the unified `jobs` table;
// the historical `artlist_runs` table has been dropped.
func (a *JobAdapter) GetJobByRunID(ctx context.Context, runID string) (*job.Job, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, fmt.Errorf("run_id is required")
	}

	job, err := a.service.jobsSvc.Get(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("lookup job %s in jobs table: %w", runID, err)
	}
	return job, nil
}
