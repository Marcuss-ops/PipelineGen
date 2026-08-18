package artlist

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// JobCodec handles conversion between Artlist types and job payload/result maps.
type JobCodec struct{}

// PayloadFromRequest converts RunTagRequest to a map suitable for job payload.
func (c *JobCodec) PayloadFromRequest(req *RunTagRequest) map[string]any {
	if req == nil {
		return map[string]any{}
	}
	m := map[string]any{
		"term":           strings.TrimSpace(req.Term),
		"limit":          req.Limit,
		"root_folder_id": strings.TrimSpace(req.RootFolderID),
		"strategy":       strings.TrimSpace(req.Strategy),
		"dry_run":        req.DryRun,
	}
	if req.ClipDuration > 0 {
		m["clip_duration"] = req.ClipDuration
	}
	if req.Width > 0 {
		m["width"] = req.Width
	}
	if req.Height > 0 {
		m["height"] = req.Height
	}
	if req.FPS > 0 {
		m["fps"] = req.FPS
	}
	if req.Concurrency > 0 {
		m["concurrency"] = req.Concurrency
	}
	return m
}

func (c *JobCodec) RequestFromPayload(payload map[string]any) *RunTagRequest {
	req := &RunTagRequest{}
	if v, ok := payload["term"].(string); ok {
		req.Term = strings.TrimSpace(v)
	}
	if v, ok := payload["limit"].(float64); ok {
		req.Limit = int(v)
	} else if v, ok := payload["limit"].(int); ok {
		req.Limit = v
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
	if v, ok := payload["clip_duration"].(float64); ok {
		req.ClipDuration = int(v)
	} else if v, ok := payload["clip_duration"].(int); ok {
		req.ClipDuration = v
	}
	if v, ok := payload["width"].(float64); ok {
		req.Width = int(v)
	} else if v, ok := payload["width"].(int); ok {
		req.Width = v
	}
	if v, ok := payload["height"].(float64); ok {
		req.Height = int(v)
	} else if v, ok := payload["height"].(int); ok {
		req.Height = v
	}
	if v, ok := payload["fps"].(float64); ok {
		req.FPS = int(v)
	} else if v, ok := payload["fps"].(int); ok {
		req.FPS = v
	}
	if v, ok := payload["concurrency"].(float64); ok {
		req.Concurrency = int(v)
	} else if v, ok := payload["concurrency"].(int); ok {
		req.Concurrency = v
	}
	return req
}

func (c *JobCodec) ResultFromResponse(resp *RunTagResponse) map[string]any {
	result := map[string]any{
		"requested":      resp.Requested,
		"found":          resp.Found,
		"processed":      resp.Processed,
		"skipped":        resp.Skipped,
		"failed":         resp.Failed,
		"would_process":  resp.WouldProcess,
		"would_skip":     resp.WouldSkip,
		"estimated_size": resp.EstimatedSize,
		"tag_folder_id":  resp.TagFolderID,
		"term":           resp.Term,
		"strategy":       resp.Strategy,
	}
	if resp.LastProcessedAt != nil {
		result["last_processed_at"] = *resp.LastProcessedAt
	}
	if len(resp.Items) > 0 {
		items := make([]map[string]any, 0, len(resp.Items))
		for _, item := range resp.Items {
			items = append(items, map[string]any{
				"clip_id":       item.ClipID,
				"name":          item.Name,
				"filename":      item.Filename,
				"status":        item.Status,
				"drive_link":    item.DriveLink,
				"drive_file_id": item.DriveFileID,
				"download_link": item.DownloadLink,
				"local_path":    item.LocalPath,
				"file_hash":     item.FileHash,
				"error":         item.Error,
			})
		}
		result["items"] = items
	}
	return result
}

func addItemFromMap(resp *RunTagResponse, itemMap map[string]any) {
	item := RunTagItem{}
	if v, ok := itemMap["clip_id"].(string); ok {
		item.ClipID = v
	}
	if v, ok := itemMap["name"].(string); ok {
		item.Name = v
	}
	if v, ok := itemMap["filename"].(string); ok {
		item.Filename = v
	}
	if v, ok := itemMap["status"].(string); ok {
		item.Status = v
	}
	if v, ok := itemMap["drive_link"].(string); ok {
		item.DriveLink = v
	}
	if v, ok := itemMap["drive_file_id"].(string); ok {
		item.DriveFileID = v
	}
	if v, ok := itemMap["download_link"].(string); ok {
		item.DownloadLink = v
	}
	if v, ok := itemMap["local_path"].(string); ok {
		item.LocalPath = v
	}
	if v, ok := itemMap["file_hash"].(string); ok {
		item.FileHash = v
	}
	if v, ok := itemMap["error"].(string); ok {
		item.Error = v
	}
	resp.Items = append(resp.Items, item)
}

// ResponseFromJob converts a kernel job.Job to RunTagResponse.
func (c *JobCodec) ResponseFromJob(j *job.Job) *RunTagResponse {
	resp := &RunTagResponse{
		OK:        j.Status != job.StatusFailed,
		RunID:     j.ID,
		Status:    string(j.Status),
		Error:     j.Error,
		Found:     0,
		Processed: 0,
		Skipped:   0,
		Failed:    0,
	}
	if j.StartedAt != nil {
		started := j.StartedAt.Format("2006-01-02T15:04:05Z07:00")
		resp.StartedAt = &started
	}
	if j.CompletedAt != nil {
		ended := j.CompletedAt.Format("2006-01-02T15:04:05Z07:00")
		resp.EndedAt = &ended
	}
	if j.Payload != nil {
		var payload map[string]any
		if err := json.Unmarshal(j.Payload, &payload); err == nil {
			if v, ok := payload["term"].(string); ok {
				resp.Term = v
			}
			if v, ok := payload["strategy"].(string); ok {
				resp.Strategy = v
			}
			if v, ok := payload["dry_run"].(bool); ok {
				resp.DryRun = v
			}
			if v, ok := payload["root_folder_id"].(string); ok {
				resp.RootFolderID = v
			}
			resp.Requested = getIntFromResult(payload, "limit")
		}
	}
	if len(j.Result) > 0 {
		var result map[string]any
		if err := json.Unmarshal(j.Result, &result); err == nil {
			resp.Requested = getIntFromResult(result, "requested")
			resp.Found = getIntFromResult(result, "found")
			resp.Processed = getIntFromResult(result, "processed")
			resp.Skipped = getIntFromResult(result, "skipped")
			resp.Failed = getIntFromResult(result, "failed")
			resp.WouldProcess = getIntFromResult(result, "would_process")
			resp.WouldSkip = getIntFromResult(result, "would_skip")
			resp.EstimatedSize = getIntFromResult(result, "estimated_size")
			if v, ok := result["tag_folder_id"].(string); ok {
				resp.TagFolderID = v
			}
			if v, ok := result["last_processed_at"].(string); ok {
				resp.LastProcessedAt = &v
			}
			if itemsRaw, ok := result["items"].([]any); ok {
				for _, itemRaw := range itemsRaw {
					if itemMap, ok := itemRaw.(map[string]any); ok {
						addItemFromMap(resp, itemMap)
					}
				}
			} else if itemsRaw, ok := result["items"].([]map[string]any); ok {
				for _, itemMap := range itemsRaw {
					addItemFromMap(resp, itemMap)
				}
			}
		}
	}
	return resp
}

// buildRunRecordFromResponse assembles a RunRecord from the worker-context
// job ID + the orchestrator's RunTagResponse.
func buildRunRecordFromResponse(jobID string, resp *RunTagResponse) RunRecord {
	rec := RunRecord{
		RunID:        jobID,
		Term:         resp.Term,
		RootFolderID: resp.RootFolderID,
		TagFolderID:  resp.TagFolderID,
		RequestedN:   resp.Requested,
		FoundN:       resp.Found,
		ProcessedN:   resp.Processed,
		SkippedN:     resp.Skipped,
		FailedN:      resp.Failed,
		Status:       "completed",
	}
	if !resp.OK || resp.Error != "" {
		rec.Status = "failed"
		rec.ErrorMessage = resp.Error
	}
	return rec
}

// JobHandler processes media.artlist jobs.
func (s *Service) JobHandler(ctx context.Context, j *job.Job, tools *appjobs.JobExecutionTools) (appjobs.Result, error) {
	codec := &JobCodec{}
	var payload map[string]any
	if err := json.Unmarshal(j.Payload, &payload); err != nil {
		return nil, fmt.Errorf("decode artlist job payload: %w", err)
	}
	req := codec.RequestFromPayload(payload)
	resp, err := s.RunTag(ctx, req)
	if err != nil {
		s.log.Error("artlist job failed", zap.String("job_id", j.ID), zap.Error(err))
		return nil, err
	}
	return codec.ResultFromResponse(resp), nil
}
