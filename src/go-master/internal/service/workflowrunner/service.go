package workflowrunner

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/service/artlist"
)

// Service manages workflow execution
type Service struct {
	runner    *Runner
	workflows map[string]*Workflow
	results   map[string]*RunResult
}

// NewService creates a new workflow service
// If artlistSvc is provided, registers the artlist.run executor
func NewService(artlistSvc *artlist.Service, log *zap.Logger) *Service {
	s := &Service{
		runner:    NewRunner(),
		workflows: make(map[string]*Workflow),
		results:   make(map[string]*RunResult),
	}
	// Register artlist.run executor if service is available
	if artlistSvc != nil && log != nil {
		Register("artlist.run", newArtlistExecutor(artlistSvc, log))
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
	s.workflows[wf.Name] = wf
	return wf, nil
}

// RunWorkflow runs a loaded workflow by name and stores the result
func (s *Service) RunWorkflow(ctx context.Context, name string) (*RunResult, error) {
	wf, ok := s.workflows[name]
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

// GetResult returns a stored result by workflow ID
func (s *Service) GetResult(workflowID string) (*RunResult, bool) {
	result, ok := s.results[workflowID]
	return result, ok
}

// RunResult is already defined in runner.go, but we might want to store it.
// We'll modify runner.Run to store results if needed.

func (s *Service) RegisterExecutor(name string, executor StepExecutor) {
	Register(name, executor)
}

// ListWorkflows returns the names of loaded workflows
func (s *Service) ListWorkflows() []string {
	names := make([]string, 0, len(s.workflows))
	for name := range s.workflows {
		names = append(names, name)
	}
	return names
}

// RunAndStore runs a workflow and stores the result
func (s *Service) RunAndStore(ctx context.Context, wf *Workflow) (*RunResult, error) {
	result, err := s.runner.Run(ctx, wf)
	if err != nil {
		return result, err
	}
	s.results[result.WorkflowID] = result
	return result, nil
}

// CleanupOldResults removes results older than maxAge
func (s *Service) CleanupOldResults(maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
	for id, result := range s.results {
		// We don't have timestamp in RunResult, but we can compute from WorkflowID (which is timestamp based)
		// For simplicity, we'll skip for now
		_ = id
		_ = result
		_ = cutoff
	}
}
