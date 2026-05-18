package artlist

import (
	"encoding/json"
	"strings"

	"velox/go-master/internal/media/models"
)

// JobCodec handles conversion between Artlist types and job payload/result maps.
// This eliminates duplicate conversion logic across job_handler.go, job_adapter.go, and run_management.go.
type JobCodec struct{}

// PayloadFromRequest converts RunTagRequest to a map suitable for job payload.
func (c *JobCodec) PayloadFromRequest(req *RunTagRequest) map[string]any {
	m := map[string]any{
		"term":           strings.TrimSpace(req.Term),
		"limit":          req.Limit,
		"root_folder_id": req.RootFolderID,
		"strategy":       req.Strategy,
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
	return m
}

// RequestFromPayload converts a job payload map to RunTagRequest.
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
	return req
}

// RequestFromJob extracts RunTagRequest from a models.Job.
func (c *JobCodec) RequestFromJob(job *models.Job) *RunTagRequest {
	if job.Payload == nil {
		return &RunTagRequest{}
	}
	var payload map[string]any
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return &RunTagRequest{}
	}
	return c.RequestFromPayload(payload)
}

// ResultFromResponse converts RunTagResponse to a map suitable for job result.
func (c *JobCodec) ResultFromResponse(resp *RunTagResponse) map[string]any {
	result := map[string]any{
		"found":          resp.Found,
		"processed":      resp.Processed,
		"skipped":        resp.Skipped,
		"failed":         resp.Failed,
		"estimated_size": resp.EstimatedSize,
		"tag_folder_id":  resp.TagFolderID,
		"term":           resp.Term,
		"strategy":       resp.Strategy,
	}
	if resp.LastProcessedAt != nil {
		result["last_processed_at"] = *resp.LastProcessedAt
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

// addItemFromMap adds an item to resp.Items from a map
func addItemFromMap(resp *RunTagResponse, itemMap map[string]interface{}) {
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

// ResponseFromJob converts a models.Job to RunTagResponse.
func (c *JobCodec) ResponseFromJob(job *models.Job) *RunTagResponse {
	resp := &RunTagResponse{
		OK:        job.Status != models.StatusFailed,
		RunID:     job.ID,
		Status:    string(job.Status),
		Error:     job.Error,
		Found:     0,
		Processed: 0,
		Skipped:   0,
		Failed:    0,
	}

	if job.StartedAt != nil {
		started := job.StartedAt.Format("2006-01-02T15:04:05Z07:00")
		resp.StartedAt = &started
	}
	if job.CompletedAt != nil {
		ended := job.CompletedAt.Format("2006-01-02T15:04:05Z07:00")
		resp.EndedAt = &ended
	}

	// Extract fields from payload
	if job.Payload != nil {
		var payload map[string]any
		if err := json.Unmarshal(job.Payload, &payload); err == nil {
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
		}
	}

	// Extract fields from result
	if job.Result != nil {
		resp.Found = getIntFromResult(job.Result, "found")
		resp.Processed = getIntFromResult(job.Result, "processed")
		resp.Skipped = getIntFromResult(job.Result, "skipped")
		resp.Failed = getIntFromResult(job.Result, "failed")
		resp.EstimatedSize = getIntFromResult(job.Result, "estimated_size")
		if v, ok := job.Result["tag_folder_id"].(string); ok {
			resp.TagFolderID = v
		}
		if v, ok := job.Result["last_processed_at"].(string); ok {
			resp.LastProcessedAt = &v
		}
		// Extract items from result
		if job.Result != nil {
			// Handle both []interface{} (from JSON) and []map[string]interface{} (direct assignment)
			if itemsRaw, ok := job.Result["items"].([]interface{}); ok {
				for _, itemRaw := range itemsRaw {
					if itemMap, ok := itemRaw.(map[string]interface{}); ok {
						addItemFromMap(resp, itemMap)
					}
				}
			} else if itemsRaw, ok := job.Result["items"].([]map[string]interface{}); ok {
				for _, itemMap := range itemsRaw {
					addItemFromMap(resp, itemMap)
				}
			}
		}
	}

	return resp
}
