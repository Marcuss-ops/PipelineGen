package workflowrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/core/jobs"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/pkg/models"
)

// Service manages workflow execution
type Service struct {
	runner    *Runner
	workflows map[string]*Workflow
	mu        sync.RWMutex
	log       *zap.Logger
}

// NewService creates a new workflow service
// If jobsSvc is provided, registers the artlist.run executor
func NewService(jobsSvc *jobservice.Service, log *zap.Logger) *Service {
	s := &Service{
		runner:    NewRunner(),
		workflows: make(map[string]*Workflow),
		log:       log,
	}
	// Register artlist.run executor if jobs service is available
	if jobsSvc != nil && log != nil {
		Register("artlist.run", newArtlistExecutorV2(jobsSvc, log))
	}
	return s
}

// LoadWorkflow loads a workflow from a YAML file
func (s *Service) LoadWorkflow(path string) (*Workflow, error) {
	wf, err := LoadFromFile(path)
	if err != nil {
		return nil, err
	}
	if err := wf.Validate(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.workflows[wf.Name] = wf
	s.mu.Unlock()
	return wf, nil
}

// RunWorkflow runs a loaded workflow by name and stores the result
func (s *Service) RunWorkflow(ctx context.Context, name string) (*RunResult, error) {
	s.mu.RLock()
	wf, ok := s.workflows[name]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("workflow not found: %s", name)
	}
	return s.RunAndStore(ctx, wf)
}

// RunWorkflowFromFile runs a workflow directly from a file and stores the result
func (s *Service) RunWorkflowFromFile(ctx context.Context, path string) (*RunResult, error) {
	wf, err := LoadFromFile(path)
	if err != nil {
		return nil, err
	}
	if err := wf.Validate(); err != nil {
		return nil, err
	}
	return s.RunAndStore(ctx, wf)
}

// RegisterExecutor registers a step executor by name
func (s *Service) RegisterExecutor(name string, executor StepExecutor) {
	Register(name, executor)
}

// Removed duplicate ListWorkflows - defined later in the file

// RunAndStore runs a workflow and returns the result
func (s *Service) RunAndStore(ctx context.Context, wf *Workflow) (*RunResult, error) {
	result, err := s.runner.Run(ctx, wf)
	if err != nil {
		return result, err
	}
	result.CompletedAt = time.Now()
	return result, nil
}

// ListWorkflows returns the names of loaded workflows
func (s *Service) ListWorkflows() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.workflows))
	for name := range s.workflows {
		names = append(names, name)
	}
	return names
}

// HandleJob handles a workflow job from the job system.
// This is the integration point between workflow runner and the job system.
func (s *Service) HandleJob(ctx context.Context, job *models.Job, tools *jobservice.JobTools) (map[string]any, error) {
	s.log.Info("handling workflow job",
		zap.String("job_id", job.ID),
		zap.String("type", string(job.Type)),
	)

	// Extract workflow name or path from payload (json.RawMessage)
	var payload map[string]any
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
		}
	}

	// Check if running from file path
	if path, ok := payload["path"].(string); ok && path != "" {
		s.log.Info("running workflow from file", zap.String("path", path))
		result, err := s.RunWorkflowFromFile(ctx, path)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"workflow_id": result.WorkflowID,
			"status":      result.Status,
			"duration":    result.Duration.String(),
			"source":      "file",
		}, nil
	}

	// Otherwise, run by workflow name
	workflowName := ""
	if v, ok := payload["workflow"].(string); ok {
		workflowName = v
	}
	if workflowName == "" {
		return nil, fmt.Errorf("workflow name or path is required in payload")
	}

	// Get the workflow by name
	s.mu.RLock()
	wf, ok := s.workflows[workflowName]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("workflow not found: %s", workflowName)
	}

	// Run the workflow
	result, err := s.RunAndStore(ctx, wf)
	if err != nil {
		return nil, err
	}

	// Return result as map
	return map[string]any{
		"workflow_id": result.WorkflowID,
		"status":      result.Status,
		"duration":    result.Duration.String(),
		"source":      "named",
	}, nil
}

// RegisterJobHandler registers the workflow job handler with the dispatcher.
func (s *Service) RegisterJobHandler(jobsSvc *jobservice.Service) {
	if jobsSvc != nil {
		// Convert core/jobs.JobType to models.JobType (both are string underlying)
		jobType := models.JobType(jobs.JobTypeWorkflowRun)
		jobsSvc.RegisterHandler(jobType, s.HandleJob)
	}
}
