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

	// Jobs table is the source of truth. Legacy artlist_runs is read-only.

	// Build result map with items for detailed tracking
	resultMap := map[string]any{
		"found":         resp.Found,
		"processed":     resp.Processed,
		"skipped":       resp.Skipped,
		"failed":        resp.Failed,
		"tag_folder_id": resp.TagFolderID,
		"term":          resp.Term,
		"strategy":      resp.Strategy,
	}

	// Include items with detailed status
	if len(resp.Items) > 0 {
		items := make([]map[string]interface{}, 0, len(resp.Items))
		for _, item := range resp.Items {
			items = append(items, map[string]interface{}{
				"clip_id":       item.ClipID,
				"name":          item.Name,
				"filename":      item.Filename,
				"status":        item.Status,
				"drive_link":    item.DriveLink,
				"download_link": item.DownloadLink,
				"local_path":    item.LocalPath,
				"file_hash":     item.FileHash,
				"error":         item.Error,
			})
		}
		resultMap["items"] = items
	}

	return resultMap, nil
}
