package artlist

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"velox/go-master/internal/service/jobs"
	"velox/go-master/pkg/models"
)

var jobCodec = &JobCodec{}

func (s *Service) HandleJob(ctx context.Context, job *models.Job, tools *jobs.JobTools) (map[string]any, error) {
	s.log.Info("handling artlist job",
		zap.String("job_id", job.ID),
		zap.String("type", string(job.Type)),
	)

	// Use codec to extract request from job payload
	req := jobCodec.RequestFromJob(job)

	// Normalize the request (worker path)
	rootFolderID := ""
	if s.driveService != nil {
		rootFolderID = s.driveService.GetDriveFolderID()
	}
	normalized := NormalizeRunTagRequest(*req, RunDefaults{
		DefaultRootFolderID: rootFolderID,
		MaxLimit:           500,
	})
	req = &normalized

	resp, err := s.RunTag(ctx, req)
	if err != nil || (resp != nil && !resp.OK) {
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		} else if resp != nil {
			errMsg = resp.Error
		}
		if errMsg == "" {
			errMsg = "unknown error"
		}
		tools.Event("error", "artlist run failed", map[string]any{
			"error": errMsg,
		})
		return nil, fmt.Errorf("%s", errMsg)
	}

	// Policy: if all items failed, mark job as failed
	if resp != nil && resp.Failed > 0 && resp.Processed == 0 && resp.Skipped == 0 {
		errMsg := "all artlist items failed"
		tools.Event("error", errMsg, map[string]any{
			"failed": resp.Failed,
		})
		return nil, fmt.Errorf("%s", errMsg)
	}

	tools.Event("completed", "artlist run completed", map[string]any{
		"found":     resp.Found,
		"processed": resp.Processed,
		"skipped":   resp.Skipped,
		"failed":    resp.Failed,
	})

	// Use codec to convert response to result map
	return jobCodec.ResultFromResponse(resp), nil
}
