package contentpackage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/pkg/models"
)

// Service handles content package job processing.
type Service struct {
	log *zap.Logger
}

// NewService creates a new ContentPackageService.
func NewService(log *zap.Logger) *Service {
	return &Service{
		log: log,
	}
}

// HandleJob processes a content package job.
// Implements the job handler interface: func(ctx context.Context, job *models.Job, tools *jobservice.JobTools) (map[string]any, error)
func (s *Service) HandleJob(ctx context.Context, job *models.Job, tools *jobservice.JobTools) (map[string]any, error) {
	s.log.Info("handling content package job",
		zap.String("job_id", job.ID),
		zap.String("type", string(job.Type)),
	)

	// Extract payload
	var payload struct {
		Title  string `json:"title"`
		Style  string `json:"style"`
		Assets string `json:"assets"`
		Output string `json:"output"`
	}
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
		}
	}

	if payload.Title == "" {
		return nil, fmt.Errorf("title is required in payload")
	}

	// Update progress
	if tools.Progress != nil {
		tools.Progress(10, "Starting content package creation")
	}

	startTime := time.Now()

	// TODO: Implement actual content package logic:
	// 1. Generate script doc based on title + style
	// 2. Find or process assets
	// 3. Upload to Drive
	// 4. Return links and status

	// For now, return a skeleton result
	result := map[string]any{
		"job_id":      job.ID,
		"title":       payload.Title,
		"style":       payload.Style,
		"assets":      payload.Assets,
		"output":      payload.Output,
		"status":      "completed",
		"duration":    time.Since(startTime).String(),
		"script_doc":  fmt.Sprintf("Script for: %s", payload.Title),
		"asset_jobs":  []string{},
		"drive_links": []string{},
		"message":     "Content package job handled (skeleton implementation)",
	}

	if tools.Progress != nil {
		tools.Progress(100, "Content package job completed")
	}

	if tools.Event != nil {
		tools.Event("completed", "Content package job completed", map[string]any{
			"title": payload.Title,
		})
	}

	s.log.Info("content package job completed",
		zap.String("job_id", job.ID),
		zap.Duration("duration", time.Since(startTime)),
	)

	return result, nil
}

// RegisterHandler registers this service as a handler for content.package jobs.
func (s *Service) RegisterHandler(jobsSvc *jobservice.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(models.JobTypeContentPackage, s.HandleJob)
		s.log.Info("registered content package job handler", zap.String("type", string(models.JobTypeContentPackage)))
	}
}
