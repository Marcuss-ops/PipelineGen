package contentpackage

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"
	"velox/go-master/internal/core/jobs"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/pkg/models"
)

// ScriptService interface for generating script docs
type ScriptService interface {
	GenerateScriptDoc(ctx context.Context, title string) (string, error)
}

// Service handles content package job processing.
type Service struct {
	log          *zap.Logger
	scriptSvc    ScriptService
	jobsSvc      *jobservice.Service
}

// NewService creates a new ContentPackageService.
func NewService(log *zap.Logger, scriptSvc ScriptService, jobsSvc *jobservice.Service) *Service {
	return &Service{
		log:       log,
		scriptSvc: scriptSvc,
		jobsSvc:   jobsSvc,
	}
}

// HandleJob processes a content package job.
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

	// Step 1: Generate script doc from title
	if tools.Progress != nil {
		tools.Progress(20, "Generating script document")
	}

	var scriptDoc string
	if s.scriptSvc != nil {
		var err error
		scriptDoc, err = s.scriptSvc.GenerateScriptDoc(ctx, payload.Title)
		if err != nil {
			s.log.Warn("failed to generate script doc", zap.Error(err))
			scriptDoc = fmt.Sprintf("Script for: %s (generation failed: %v)", payload.Title, err)
		}
	} else {
		// Enqueue a script.generate job if jobsSvc is available
		if s.jobsSvc != nil {
			scriptJob, err := s.jobsSvc.Enqueue(ctx, &jobservice.EnqueueRequest{
				Type:    models.JobType(jobs.JobTypeScriptGenerate),
				Payload: map[string]any{"topic": payload.Title, "language": "it"},
			})
			if err != nil {
				s.log.Warn("failed to enqueue script job", zap.Error(err))
			} else {
				s.log.Info("enqueued script job", zap.String("script_job_id", scriptJob.ID))
				if tools.Event != nil {
					tools.Event("script_job_enqueued", "Script generation job enqueued", map[string]any{
						"script_job_id": scriptJob.ID,
					})
				}
			}
		}
		scriptDoc = fmt.Sprintf("Script for: %s (enqueued for generation)", payload.Title)
	}

	result := map[string]any{
		"job_id":     job.ID,
		"title":      payload.Title,
		"style":      payload.Style,
		"assets":     payload.Assets,
		"output":     payload.Output,
		"status":     "completed",
		"script_doc": scriptDoc,
		"message":    "Content package job completed (first step: script doc)",
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
