package scriptgeneration

import (
	"context"
	"testing"
)

func TestGenerationRunStarterFailRunOnPanic(t *testing.T) {
	repo := newInMemRunRepository()
	run := &GenerationRun{
		ID:           "run_panic",
		Status:       RunStatusRunning,
		CurrentStage: StageGeneratingSceneText,
	}
	if err := repo.Create(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	starter := &GenerationRunStarter{repo: repo}
	if err := starter.failRunOnPanic(run.ID, "boom"); err != nil {
		t.Fatalf("persist panic failure: %v", err)
	}

	got, err := repo.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("read run: %v", err)
	}
	if got.Status != RunStatusFailed {
		t.Fatalf("status = %q, want %q", got.Status, RunStatusFailed)
	}
	if got.FailedStage != StageGeneratingSceneText {
		t.Fatalf("failed stage = %q, want %q", got.FailedStage, StageGeneratingSceneText)
	}
	if got.ErrorCode != "RUNNER_PANIC" {
		t.Fatalf("error code = %q, want RUNNER_PANIC", got.ErrorCode)
	}
	if got.AttemptCount != 1 {
		t.Fatalf("attempt count = %d, want 1", got.AttemptCount)
	}
}
