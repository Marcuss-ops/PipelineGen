package jobs

import (
	"context"
	"encoding/json"
	"fmt"

	jobtools "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"

	"go.uber.org/zap"
)

// YouTubeExtractor is the bounded interface this handler needs from the
// usecase layer. The concrete usecase.Service satisfies it. Using this
// interface instead of importing youtube/usecase (which would create a
// cycle because usecase/orchestrator.go imports this jobs package).
type YouTubeExtractor interface {
	Extract(ctx context.Context, req *youtubetypes.ExtractRequest) (*youtubetypes.ExtractResponse, error)
}

// JobHandler wraps the usecase.Service to satisfy the job-system
// handler signature (func(ctx, *job.Job, *JobTools) (map[string]any, error)).
// Uses the local YouTubeExtractor interface instead of importing
// youtube/usecase, breaking the usecase↔jobs import cycle.
type JobHandler struct {
	svc YouTubeExtractor
	log *zap.Logger
}

// NewJobHandler constructs a job-system handler for youtube_clip.extract jobs.
func NewJobHandler(svc YouTubeExtractor, log *zap.Logger) *JobHandler {
	return &JobHandler{svc: svc, log: log}
}

// HandleJob processes a youtube_clip.extract job.
func (h *JobHandler) HandleJob(ctx context.Context, job *jobservice.Job, tools *jobtools.JobTools) (map[string]any, error) {
	h.log.Info("handling youtube_clip.extract job",
		zap.String("job_id", job.ID),
	)

	var req youtubetypes.ExtractRequest
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &req); err != nil {
			return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
		}
	}

	h.log.Info("YouTube extract job payload parsed",
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

	resp, err := h.svc.Extract(ctx, &req)
	if err != nil {
		h.log.Warn("YouTube extract job failed",
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

	h.log.Info("YouTube extract job result",
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
