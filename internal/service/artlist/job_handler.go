package artlist

import (
	"context"
	"encoding/json"
	"strings"

	"go.uber.org/zap"
	"velox/go-master/internal/service/jobs"
	"velox/go-master/pkg/models"
)

func (s *Service) HandleJob(ctx context.Context, job *models.Job, tools *jobs.JobTools) (map[string]any, error) {
	s.log.Info("handling artlist job",
		zap.String("job_id", job.ID),
		zap.String("type", string(job.Type)),
	)

	var payload map[string]any
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return nil, err
		}
	}

	req := &RunTagRequest{}
	if v, ok := payload["term"].(string); ok {
		req.Term = strings.TrimSpace(v)
	}
	if v, ok := payload["limit"].(float64); ok {
		req.Limit = int(v)
	}
	if v, ok := payload["root_folder_id"].(string); ok {
		req.RootFolderID = strings.TrimSpace(v)
	}
	if v, ok := payload["strategy"].(string); ok {
		req.Strategy = v
	}
	if v, ok := payload["dry_run"].(bool); ok {
		req.DryRun = v
	}

	resp, err := s.RunTag(ctx, req)
	if err != nil || (resp != nil && !resp.OK) {
		tools.Event("error", "artlist run failed", map[string]any{
			"error": resp.Error,
		})
		return nil, err
	}

	tools.Event("completed", "artlist run completed", map[string]any{
		"found":     resp.Found,
		"processed": resp.Processed,
		"skipped":   resp.Skipped,
		"failed":    resp.Failed,
	})

	return map[string]any{
		"found":         resp.Found,
		"processed":     resp.Processed,
		"skipped":       resp.Skipped,
		"failed":        resp.Failed,
		"tag_folder_id": resp.TagFolderID,
		"term":          resp.Term,
		"strategy":      resp.Strategy,
	}, nil
}
