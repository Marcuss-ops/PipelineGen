package artlist

import (
	"context"
	"fmt"
	"strings"

	job "github.com/Marcuss-ops/PipelineGen/internal/jobs"
)

func (a *JobAdapter) GetJobByRunID(ctx context.Context, runID string) (*job.Job, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, fmt.Errorf("run_id is required")
	}

	res, err := a.service.jobsSvc.Get(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("lookup job %s in jobs table: %w", runID, err)
	}
	return res, nil
}
