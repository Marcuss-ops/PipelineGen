package artlist

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

var jobCodec = &JobCodec{}

func (a *JobAdapter) HandleJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	s := a.service
	s.log.Info("handling artlist job",
		zap.String("job_id", j.ID),
		zap.String("type", j.Type),
	)

	// Extract request from job payload directly (domain *job.Job)
	var payloadMap map[string]any
	if err := json.Unmarshal(j.Payload, &payloadMap); err != nil {
		payloadMap = map[string]any{}
	}
	req := jobCodec.RequestFromPayload(payloadMap)

	// Normalize the request (worker path)
	normalized := NormalizeRunTagRequest(*req, RunDefaults{
		DefaultRootFolderID: ResolveRootFolderID(s.cfg),
		MaxLimit:            500,
	})
	req = &normalized

	if strings.TrimSpace(req.RootFolderID) == "" {
		s.log.Warn("skipping artlist job because no root folder is configured",		zap.String("job_id", j.ID), zap.String("term", req.Term))
		tools.Event("warning", "artlist job skipped: no root folder configured", map[string]any{
			"term": req.Term,
		})
		return map[string]any{
			"skipped": 1,
			"reason":  "no root folder configured",
		}, nil
	}

	resp, err := s.runOrchestrator.RunTag(ctx, req)
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

	// Use centralized policy evaluation
	if failed, errMsg := EvaluateRunOutcome(resp); failed {
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
