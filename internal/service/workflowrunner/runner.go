package workflowrunner

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// Runner executes a workflow
type Runner struct {
	registry *Registry
}

// buildLevels performs topological sort and groups steps into levels
func buildLevels(steps []Step) ([][]Step, error) {
	byID := make(map[string]Step)
	indegree := make(map[string]int)
	children := make(map[string][]string)

	for _, step := range steps {
		if step.ID == "" {
			return nil, fmt.Errorf("step id is required")
		}
		byID[step.ID] = step
		indegree[step.ID] = len(step.Needs)

		for _, dep := range step.Needs {
			children[dep] = append(children[dep], step.ID)
		}
	}

	var levels [][]Step

	for len(indegree) > 0 {
		var level []Step

		for id, deg := range indegree {
			if deg == 0 {
				level = append(level, byID[id])
			}
		}

		if len(level) == 0 {
			return nil, fmt.Errorf("cycle detected in workflow")
		}

		for _, step := range level {
			delete(indegree, step.ID)

			for _, child := range children[step.ID] {
				indegree[child]--
			}
		}

		levels = append(levels, level)
	}

	return levels, nil
}

// NewRunner creates a new workflow runner
func NewRunner() *Runner {
	r := &Runner{
		registry: defaultRegistry,
	}
	// Register http executor for http and http_job types
	httpExec := newHTTPExecutor()
	RegisterService("http", httpExec.Execute)
	RegisterService("http_job", httpExec.Execute)
	return r
}

// RunResult contains the result of a workflow run
type RunResult struct {
	WorkflowName string
	WorkflowID   string
	Status       string
	StepResults  map[string]*StepOutput
	Error        string
	Duration     time.Duration
	CreatedAt    time.Time
	CompletedAt  time.Time
}

// Run executes a workflow using DAG-based execution
func (r *Runner) Run(ctx context.Context, wf *Workflow) (*RunResult, error) {
	start := time.Now()
	result := &RunResult{
		WorkflowName: wf.Name,
		WorkflowID:   fmt.Sprintf("wf_%d", time.Now().UnixNano()),
		Status:       "running",
		StepResults:  make(map[string]*StepOutput),
		CreatedAt:    time.Now(),
	}

	// Build levels for DAG execution
	levels, err := buildLevels(wf.Steps)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		result.Duration = time.Since(start)
		return result, err
	}

	state := &WorkflowState{
		WorkflowID:  result.WorkflowID,
		Status:      "running",
		StepOutputs: make(map[string]*StepOutput),
		CurrentStep: 0,
	}

	// Apply defaults to steps if not specified
	for i := range wf.Steps {
		if wf.Steps[i].Payload == nil {
			wf.Steps[i].Payload = make(map[string]interface{})
		}
		// Merge defaults if applicable
		// TODO: implement default merging
	}

	// Execute levels in order, parallel within each level
	var mu sync.Mutex // Protects concurrent writes to result.StepResults and state.StepOutputs

	for _, level := range levels {
		errGroup, ctx := errgroup.WithContext(ctx)

		for _, step := range level {
			step := step // capture loop variable
			errGroup.Go(func() error {
				// Render payload with templating
				payload, err := renderPayload(step.Payload, wf, state)
				if err != nil {
					mu.Lock()
					result.StepResults[step.ID] = &StepOutput{
						OK:     false,
						Status: "failed",
						Error:  fmt.Sprintf("failed to render payload: %v", err),
					}
					mu.Unlock()
					return fmt.Errorf("step %s: failed to render payload: %w", step.ID, err)
				}

				// For service executors (uses), also render With and merge into payload
				if len(step.With) > 0 {
					withPayload, err := renderPayload(step.With, wf, state)
					if err != nil {
						mu.Lock()
						result.StepResults[step.ID] = &StepOutput{
							OK:     false,
							Status: "failed",
							Error:  fmt.Sprintf("failed to render with: %v", err),
						}
						mu.Unlock()
						return fmt.Errorf("step %s: failed to render with: %w", step.ID, err)
					}
					// Merge with into payload
					if payload == nil {
						payload = make(map[string]interface{})
					}
					for k, v := range withPayload {
						payload[k] = v
					}
				}

				input := &StepInput{
					Workflow: wf,
					Step:     &step,
					Payload:  payload,
					State:    state,
				}

				// Execute step: try uses first, then type
				var output *StepOutput
				var execErr error

				executorName := step.Uses
				if executorName == "" {
					executorName = step.Type
				}

				executor, ok := defaultRegistry.executors[executorName]
				if !ok {
					execErr = fmt.Errorf("no executor registered for step %s (uses=%s, type=%s)", step.ID, step.Uses, step.Type)
				} else {
					output, execErr = executor.Execute(ctx, input)
				}

				if execErr != nil {
					mu.Lock()
					result.StepResults[step.ID] = &StepOutput{
						OK:     false,
						Status: "failed",
						Error:  execErr.Error(),
					}
					mu.Unlock()
					return fmt.Errorf("step %s failed: %w", step.ID, execErr)
				}

				mu.Lock()
				result.StepResults[step.ID] = output
				state.StepOutputs[step.ID] = output
				mu.Unlock()

				if !output.OK {
					return fmt.Errorf("step %s returned not ok: %s", step.ID, output.Error)
				}

				return nil
			})
		}

		// Wait for all steps in this level to complete
		if err := errGroup.Wait(); err != nil {
			result.Status = "failed"
			result.Error = err.Error()
			result.Duration = time.Since(start)
			return result, err
		}
	}

	result.Status = "completed"
	result.Duration = time.Since(start)
	return result, nil
}
