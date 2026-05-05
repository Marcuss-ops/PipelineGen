package workflowrunner

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
	jobservice "velox/go-master/internal/service/jobs"
)

// Service manages workflow execution
type Service struct {
	runner    *Runner
	workflows map[string]*Workflow
	results   map[string]*RunResult
	mu        sync.RWMutex
}

// NewService creates a new workflow service
// If jobsSvc is provided, registers the artlist.run executor
func NewService(jobsSvc *jobservice.Service, log *zap.Logger) *Service {
	s := &Service{
		runner:    NewRunner(),
		workflows: make(map[string]*Workflow),
		results:   make(map[string]*RunResult),
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

// GetResult returns a stored result by workflow ID
func (s *Service) GetResult(workflowID string) (*RunResult, bool) {
	s.mu.RLock()
	result, ok := s.results[workflowID]
	s.mu.RUnlock()
	return result, ok
}

// RunResult is already defined in runner.go, but we might want to store it.
// We'll modify runner.Run to store results if needed.

func (s *Service) RegisterExecutor(name string, executor StepExecutor) {
	Register(name, executor)
}

// Removed duplicate ListWorkflows - defined later in the file

// RunAndStore runs a workflow and stores the result
func (s *Service) RunAndStore(ctx context.Context, wf *Workflow) (*RunResult, error) {
	result, err := s.runner.Run(ctx, wf)
	if err != nil {
		return result, err
	}
	result.CompletedAt = time.Now()
	s.mu.Lock()
	s.results[result.WorkflowID] = result
	s.mu.Unlock()
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

// CleanupOldResults removes results older than maxAge
func (s *Service) CleanupOldResults(maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, result := range s.results {
		if result.CompletedAt.Before(cutoff) {
			delete(s.results, id)
		}
	}
}
