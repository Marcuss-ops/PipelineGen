package youtube

import (
	"context"
	"encoding/json"
	"fmt"

	jobtools "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"

	"go.uber.org/zap"
)

// HandleJob processes a youtube_clip.extract job.
//
// PR fix: `jobservice.JobTools` was undefined because JobTools lives
// in internal/application/jobs (not internal/domain/job, which only
// hosts the Job entity). HandlerFunc in
// internal/application/jobs/types.go has the canonical signature
//
//	func(ctx context.Context, j *job.Job, tools *JobTools)
//
// and we mirror it here.
func (s *Service) HandleJob(ctx context.Context, job *jobservice.Job, tools *jobtools.JobTools) (map[string]any, error) {
	s.log.Info("handling youtube_clip.extract job",
		zap.String("job_id", job.ID),
	)

	var req ExtractRequest
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &req); err != nil {
			return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
		}
	}

	s.log.Info("YouTube extract job payload parsed",
		zap.String("job_id", job.ID),
		zap.String("url", req.URL),
		zap.Int("segments_provided", len(req.Segments)),
		zap.String("group", func() string {
			if req.Destination != nil {
				return req.Destination.Group
			}
			return ""
		}()),
	)

	if tools.Progress != nil {
		tools.Progress(5, "Job started, preparing extraction")
	}

	resp, err := s.Extract(ctx, &req)
	if err != nil {
		s.log.Warn("YouTube extract job failed",
			zap.String("job_id", job.ID),
			zap.String("url", req.URL),
			zap.Error(err))
		return nil, fmt.Errorf("extraction failed: %w", err)
	}

	if tools.Progress != nil {
		pct := 100
		msg := "YouTube clip extraction completed"
		if !resp.OK && resp.Error != "" {
			msg = "YouTube clip extraction finished with errors"
		}
		tools.Progress(pct, msg)
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

	s.log.Info("YouTube extract job result",
		zap.String("job_id", job.ID),
		zap.Bool("ok", resp.OK),
		zap.Int("processed", resp.Stats.Processed),
		zap.Int("failed", resp.Stats.Failed),
		zap.Int("skipped", resp.Stats.Skipped),
		zap.String("drive_folder", resp.DriveFolderPath),
		zap.String("error", resp.Error),
	)

	return result, nil
}
