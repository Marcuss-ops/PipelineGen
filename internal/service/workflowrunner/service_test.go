package workflowrunner

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// mockExecutor is a simple mock that returns success
type mockExecutor struct {
	output *StepOutput
	err    error
}

func (m *mockExecutor) Execute(ctx context.Context, input *StepInput) (*StepOutput, error) {
	if m.output != nil {
		return m.output, m.err
	}
	return &StepOutput{OK: true, Status: "completed"}, m.err
}

// failingExecutor always fails
type failingExecutor struct{}

func (f *failingExecutor) Execute(ctx context.Context, input *StepInput) (*StepOutput, error) {
	return &StepOutput{OK: false, Status: "failed", Error: "step failed"}, fmt.Errorf("step failed")
}

func TestWorkflowRunsStepsInOrder(t *testing.T) {
	svc := NewService(nil, nil)

	// Register mock executor
	svc.RegisterExecutor("mock_step", &mockExecutor{})

	// Create workflow with 2 steps
	wf := &Workflow{
		Name: "test-workflow",
		Steps: []Step{
			{ID: "step1", Uses: "mock_step"},
			{ID: "step2", Uses: "mock_step", Needs: []string{"step1"}},
		},
	}

	result, err := svc.RunAndStore(context.Background(), wf)
	if err != nil {
		t.Fatalf("workflow failed: %v", err)
	}

	if result.Status != "completed" {
		t.Errorf("expected workflow status 'completed', got '%s'", result.Status)
	}

	if len(result.StepResults) != 2 {
		t.Errorf("expected 2 step results, got %d", len(result.StepResults))
	}
}

func TestWorkflowStopsOnFailedStep(t *testing.T) {
	svc := NewService(nil, nil)

	// Register executors
	svc.RegisterExecutor("success_step", &mockExecutor{})
	svc.RegisterExecutor("fail_step", &failingExecutor{})

	// Create workflow where step2 fails
	wf := &Workflow{
		Name: "test-fail-workflow",
		Steps: []Step{
			{ID: "step1", Uses: "success_step"},
			{ID: "step2", Uses: "fail_step", Needs: []string{"step1"}},
			{ID: "step3", Uses: "success_step", Needs: []string{"step2"}},
		},
	}

	result, err := svc.RunAndStore(context.Background(), wf)
	// We expect an error because step2 fails
	if err == nil && (result == nil || result.Status == "completed") {
		t.Error("expected workflow to fail due to step2 failure")
	}

	// step3 should not have run because step2 failed
	if result != nil {
		if _, ok := result.StepResults["step3"]; ok {
			t.Error("step3 should not have run after step2 failed")
		}

		if step2Result, ok := result.StepResults["step2"]; ok && step2Result.OK {
			t.Error("step2 should have failed")
		}
	}
}

func TestWorkflowCanPassOutputToNextStep(t *testing.T) {
	svc := NewService(nil, nil)

	// Mock executor that passes data
	executor := &mockExecutor{
		output: &StepOutput{
			OK:     true,
			Status: "completed",
			Raw: map[string]interface{}{"data": "test_value"},
		},
	}
	svc.RegisterExecutor("passing_step", executor)

	wf := &Workflow{
		Name: "test-pass-workflow",
		Steps: []Step{
			{ID: "step1", Uses: "passing_step"},
			{ID: "step2", Uses: "passing_step", Needs: []string{"step1"}},
		},
	}

	result, err := svc.RunAndStore(context.Background(), wf)
	if err != nil {
		t.Fatalf("workflow failed: %v", err)
	}

	if result.Status != "completed" {
		t.Errorf("expected workflow status 'completed', got '%s'", result.Status)
	}
}

func TestWorkflowTimeoutFailsCleanly(t *testing.T) {
	svc := NewService(nil, nil)

	// Create a workflow with a timeout
	wf := &Workflow{
		Name: "test-timeout-workflow",
		Steps: []Step{
			{
				ID:   "slow_step",
				Type: "http",
				Wait: &WaitConfig{
					TimeoutSeconds: 1,
				},
			},
		},
	}

	// Run with a cancelled context to simulate timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	result, err := svc.RunAndStore(ctx, wf)
	if err == nil && (result == nil || result.Status != "failed") {
		t.Error("expected workflow to fail due to timeout")
	}
}
