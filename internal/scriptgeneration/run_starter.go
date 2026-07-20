// Package scriptgeneration — run_starter.go defines the
// GenerationRunStarter which creates a GenerationRun BEFORE
// any external I/O and launches the Runner.Execute() in a
// background goroutine for durable stage execution.
//
// After the runner is launched it executes the pipeline asynchronously:
//
//	Normalize → GenerateText → Translate → Voiceover →
//	UpsertDocs → BuildPayload → EnqueueRender → Complete
//
// Verdetto § "La POST deve creare il run prima di qualsiasi I/O":
//
//	POST /api/v1/script/generate
//	  ├─ valida il contratto
//	  ├─ calcola/legge Idempotency-Key
//	  ├─ crea pipeline_run          ← starter.Start(ctx, req)
//	  ├─ registra il comando         ← submitter.Submit()
//	  ├─ avvia runner in background  ← go runner.Execute()
//	  └─ restituisce 202 Accepted    ← current_stage in response
package scriptgeneration

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// GenerationRunStarter creates a GenerationRun before the submission
// I/O and launches the Runner.Execute() in a background goroutine.
// The handler creates the run with Start(), then calls submitter.Submit(),
// then returns 202 with current_stage.
type GenerationRunStarter struct {
	runner *Runner
}

// NewGenerationRunStarter constructs the starter with a runner for
// background execution. When runner is nil, Start still creates the
// run but no background execution is launched.
func NewGenerationRunStarter(runner *Runner) *GenerationRunStarter {
	return &GenerationRunStarter{
		runner: runner,
	}
}

// Start creates a GenerationRun with the pipeline's initial stage
// and launches the Runner.Execute() in a background goroutine.
// The run is created in-memory (no DB write); the runner persist
// checkpoint stages as it executes.
//
// Returns the run so the handler can include current_stage in the
// 202 Accepted response.
func (s *GenerationRunStarter) Start(ctx context.Context, req GenerateRequest) *GenerationRun {
	now := time.Now().UTC()
	run := &GenerationRun{
		ID:           "run_" + uuid.New().String(),
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	// Launch the runner in a background goroutine.
	// The runner reads the run from repo, sets RUNNING status,
	// and executes stages with checkpoint persistence after each.
	if s.runner != nil {
		go s.runner.Execute(context.Background(), run.ID, req)
	}

	return run
}
