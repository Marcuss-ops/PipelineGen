package youtube

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/models"
)

// HandleJob processes a youtube_clip.extract job.
func (s *Service) HandleJob(ctx context.Context, job *models.Job, tools *jobservice.JobTools) (map[string]any, error) {
	s.log.Info("handling youtube_clip.extract job",
		zap.String("job_id", job.ID),
	)

	var req ExtractRequest
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &req); err != nil {
			return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
		}
	}

	if tools.Progress != nil {
		tools.Progress(10, "Starting YouTube clip extraction")
	}

	resp, err := s.Extract(ctx, &req)
	if err != nil {
		return nil, fmt.Errorf("extraction failed: %w", err)
	}

	if tools.Progress != nil {
		tools.Progress(100, "YouTube clip extraction completed")
	}

	result := map[string]any{
		"ok":              resp.OK,
		"source_url":      resp.SourceURL,
		"video_id":        resp.VideoID,
		"folder":          resp.Folder,
		"stats":           resp.Stats,
		"items":           resp.Items,
		"drive_folder_id": resp.DriveFolderID,
		"message":         "YouTube clip extraction completed",
	}
	if !resp.OK && resp.Error != "" {
		result["error"] = resp.Error
	}
	return result, nil
}
