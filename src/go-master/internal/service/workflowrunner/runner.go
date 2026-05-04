package workflowrunner

import (
	"context"
	"fmt"
	"time"
)

// Runner executes a workflow
type Runner struct {
	registry *Registry
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
	WorkflowID  string
	Status      string
	StepResults map[string]*StepOutput
	Error       string
	Duration    time.Duration
}

// Run executes a workflow sequentially
func (r *Runner) Run(ctx context.Context, wf *Workflow) (*RunResult, error) {
	start := time.Now()
	result := &RunResult{
		WorkflowName: wf.Name,
		WorkflowID:  fmt.Sprintf("wf_%d", time.Now().UnixNano()),
		Status:      "running",
		StepResults: make(map[string]*StepOutput),
	}

	state := &WorkflowState{
		WorkflowID:   result.WorkflowID,
		Status:       "running",
		StepOutputs:  make(map[string]*StepOutput),
		CurrentStep:  0,
	}

	// Apply defaults to steps if not specified
	for i := range wf.Steps {
		if wf.Steps[i].Payload == nil {
			wf.Steps[i].Payload = make(map[string]interface{})
		}
		// Merge defaults if applicable
		// TODO: implement default merging
	}

	for i, step := range wf.Steps {
		state.CurrentStep = i
		stepCtx := ctx

		// Render payload with templating
		payload, err := renderPayload(step.Payload, wf, state)
		if err != nil {
			result.Status = "failed"
			result.Error = fmt.Sprintf("step %s: failed to render payload: %v", step.ID, err)
			result.Duration = time.Since(start)
			return result, fmt.Errorf(result.Error)
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
			output, execErr = executor.Execute(stepCtx, input)
		}

		if execErr != nil {
			result.Status = "failed"
			result.Error = fmt.Sprintf("step %s failed: %v", step.ID, execErr)
			result.Duration = time.Since(start)
			return result, fmt.Errorf(result.Error)
		}

		result.StepResults[step.ID] = output
		state.StepOutputs[step.ID] = output

		// Handle async wait if configured
		if step.Wait != nil && output.Status == "running" {
			// TODO: implement wait logic for async jobs
		}

		if !output.OK {
			result.Status = "failed"
			result.Error = fmt.Sprintf("step %s returned not ok", step.ID)
			result.Duration = time.Since(start)
			return result, fmt.Errorf(result.Error)
		}
	}

	result.Status = "completed"
	result.Duration = time.Since(start)
	return result, nil
}
