// Package video provides video processing capabilities.
package video

import (
	"context"

	"velox/go-master/internal/service/pipeline"
)

// VideoProcessorAdapter wraps Processor to satisfy pipeline.VideoProcessor interface
type VideoProcessorAdapter struct {
	processor *Processor
}

// NewVideoProcessorAdapter creates a new video processor adapter for pipeline
func NewVideoProcessorAdapter(processor *Processor) *VideoProcessorAdapter {
	return &VideoProcessorAdapter{processor: processor}
}

// GenerateVideo creates a video and returns pipeline-compatible result
func (a *VideoProcessorAdapter) GenerateVideo(ctx context.Context, req pipeline.VideoGenerationRequest) (*pipeline.VideoGenerationResult, error) {
	// Convert pipeline request to video package request
	videoReq := GenerationRequest{
		JobID:         req.JobID,
		OutputPath:    req.OutputPath,
		ProjectName:   req.ProjectName,
		VideoName:     req.VideoName,
		Language:      req.Language,
		Duration:      req.Duration,
		DriveFolderID: req.DriveFolderID,
	}

	result, err := a.processor.GenerateVideo(ctx, videoReq)
	if err != nil {
		return nil, err
	}

	return &pipeline.VideoGenerationResult{
		JobID:     req.JobID,
		VideoPath: result.VideoPath,
		Status:    "created",
	}, nil
}

// Ensure VideoProcessorAdapter satisfies the VideoProcessor interface
var _ pipeline.VideoProcessor = (*VideoProcessorAdapter)(nil)
