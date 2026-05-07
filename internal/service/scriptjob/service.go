package scriptjob

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"
	"velox/go-master/internal/core/jobs"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/internal/service/scriptdocs"
	"velox/go-master/pkg/models"
)

// Service handles script.generate jobs
type Service struct {
	log      *zap.Logger
	docSvc   *scriptdocs.Service
	jobsSvc  *jobservice.Service
}

// NewService creates a new script job service
func NewService(log *zap.Logger, docSvc *scriptdocs.Service, jobsSvc *jobservice.Service) *Service {
	return &Service{
		log:     log,
		docSvc:  docSvc,
		jobsSvc: jobsSvc,
	}
}

// HandleJob processes a script.generate job
func (s *Service) HandleJob(ctx context.Context, job *models.Job, tools *jobservice.JobTools) (map[string]any, error) {
	s.log.Info("handling script.generate job",
		zap.String("job_id", job.ID),
	)

	// Decode payload
	var payload jobs.ScriptGeneratePayload
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
		}
	}

	if payload.Topic == "" {
		return nil, fmt.Errorf("topic is required in payload")
	}

	if tools.Progress != nil {
		tools.Progress(10, "Starting script generation")
	}

	// Generate script using the scriptdocs service
	if s.docSvc == nil {
		return nil, fmt.Errorf("script docs service is not available")
	}

	if tools.Progress != nil {
		tools.Progress(20, "Generating script with Ollama")
	}

	scriptID, err := s.docSvc.GenerateScript(ctx, payload.Topic, payload.Style, payload.Language)
	if err != nil {
		return nil, fmt.Errorf("failed to generate script: %w", err)
	}

	if tools.Progress != nil {
		tools.Progress(100, "Script generation completed")
	}

	result := map[string]any{
		"job_id":   job.ID,
		"script_id": scriptID,
		"topic":     payload.Topic,
		"status":    "completed",
		"message":   "Script generated successfully",
	}

	if tools.Event != nil {
		tools.Event("completed", "Script generation completed", map[string]any{
			"script_id": scriptID,
			"topic":     payload.Topic,
		})
	}

	s.log.Info("script.generate job completed",
		zap.String("job_id", job.ID),
		zap.Int64("script_id", scriptID),
	)

	return result, nil
}

// RegisterHandler registers this service as a handler for script.generate jobs
func (s *Service) RegisterHandler(jobsSvc *jobservice.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(models.JobType(jobs.JobTypeScriptGenerate), s.HandleJob)
		s.log.Info("registered script.generate job handler", zap.String("type", string(jobs.JobTypeScriptGenerate)))
	}
}
