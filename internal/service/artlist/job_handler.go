package artlist

import (
	"context"
	"encoding/json"
	"fmt"
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

	tools.Event("completed", "artlist run completed", map[string]any{
		"found":     resp.Found,
		"processed": resp.Processed,
		"skipped":   resp.Skipped,
		"failed":    resp.Failed,
	})

	// Note: In the new job system, results are tracked in jobs.db.sqlite
	// The artlist_runs table is only for legacy UUID-based records
	if resp != nil {
		activeKey := RunDedupKey(resp.Term, resp.RootFolderID, resp.Strategy, resp.DryRun)
		if rec, err := s.findRunRecordByActiveKey(ctx, activeKey); err == nil && rec != nil {
			// Update legacy artlist_runs record if it exists
			if err := s.finishRunRecord(ctx, rec.RunID, string(job.Status), resp); err != nil {
				s.log.Warn("failed to persist run record", zap.Error(err))
			}
		} else {
			s.log.Debug("no legacy artlist run record found; job status is tracked in jobs table",
				zap.String("job_id", job.ID),
				zap.String("active_key", activeKey),
			)
		}
	}

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
