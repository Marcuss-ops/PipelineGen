package scriptgeneration

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
)

func (r *Runner) submitFinalJob(ctx context.Context, runID string, req GenerateRequest, result *GenerateResult) bool {
	if r.finalJobSubmitter == nil {
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, fmt.Errorf("final_job=true but remote final-job submitter is not configured"))
		return false
	}
	remote, err := r.finalJobSubmitter.SubmitFinalJob(ctx, runID, req, result)
	if err != nil {
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, fmt.Errorf("remote final job: %w", err))
		return false
	}
	if strings.TrimSpace(remote.JobID) == "" || !strings.EqualFold(strings.TrimSpace(remote.Status), "SUCCEEDED") {
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, fmt.Errorf("remote final job returned incomplete/non-success result (job_id=%q status=%q)", remote.JobID, remote.Status))
		return false
	}
	result.RemoteFinalJob = &remote
	r.checkpoint(ctx, runID, result)
	if r.log != nil {
		r.log.Info("remote final job completed", zap.String("run_id", runID), zap.String("remote_job_id", remote.JobID), zap.String("worker_id", remote.WorkerID))
	}
	return true
}
