// Package scriptgeneration — service.go is the linear orchestrator
// for the script-generation workflow. It owns exactly one public
// method (Start) that creates the GenerationRun aggregate and
// delegates execution to the runner.
//
// Verdetto invariants:
//   - The run is created BEFORE any external I/O (Ollama, Google Docs, etc.).
//   - Start returns immediately after creating the run and registering
//     the execution command. The runner continues in the background.
//   - The HTTP handler calls Start and returns 202 Accepted with the
//     status_url pointing to the run.
//   - No network, no database, no Google Drive inside the builder.
package scriptgeneration

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Service is the single linear orchestrator for script generation.
// It is NOT an abstract plugin system or phase registry — it is a
// straightforward, readable struct with explicit fields.
type Service struct {
	// Runner executes the durable stages. Created by NewService.
	runner *Runner

	// RunRepo persists GenerationRun aggregates.
	repo RunRepository
}

// NewService constructs the canonical Service with all required ports.
// Every port is required; a nil port causes a panic at construction
// time (fail-fast per godlike/07 NO-FAKE-AVAILABILITY).
func NewService(
	repo RunRepository,
	textGen TextGenerator,
	translator Translator,
	voiceoverGen VoiceoverGenerator,
	docPublisher DocumentPublisher,
	renderEnqueuer RenderEnqueuer,
) *Service {
	if repo == nil {
		panic("scriptgeneration: RunRepository is required")
	}
	if textGen == nil {
		panic("scriptgeneration: TextGenerator is required")
	}
	if translator == nil {
		panic("scriptgeneration: Translator is required")
	}
	if docPublisher == nil {
		panic("scriptgeneration: DocumentPublisher is required")
	}
	// VoiceoverGenerator and RenderEnqueuer are conditionally required
	// — validated at Start time based on request flags.

	return &Service{
		repo: repo,
		runner: NewRunner(
			repo,
			textGen,
			translator,
			voiceoverGen,
			docPublisher,
			renderEnqueuer,
		),
	}
}

// StartResult carries the outcome of Service.Start.
type StartResult struct {
	// Run is the newly created GenerationRun.
	Run *GenerationRun

	// StatusURL is the canonical observation endpoint.
	StatusURL string
}

// Start creates a new GenerationRun and initiates the workflow.
//
// Verdetto contract:
//
//	POST /api/v1/script/generate
//	  ├─ valida il contratto
//	  ├─ calcola/legge Idempotency-Key
//	  ├─ crea pipeline_run
//	  ├─ registra il comando di esecuzione
//	  └─ restituisce 202 Accepted
//
// The runner executes the stages asynchronously. The caller (HTTP
// handler) should return 202 Accepted with the status_url.
func (s *Service) Start(ctx context.Context, req GenerateRequest) (*StartResult, error) {
	if req.IdempotencyKey == "" {
		return nil, fmt.Errorf("scriptgeneration: idempotency_key is required")
	}
	if req.Source.Type == "" {
		return nil, fmt.Errorf("scriptgeneration: source.type is required")
	}

	// Validate conditional ports at Start time.
	if req.RenderVideo && s.runner.renderEnqueuer == nil {
		return nil, fmt.Errorf("scriptgeneration: render_video requires a RenderEnqueuer, but none is configured")
	}

	// Validate document publishing config.
	// Uses ResolveDocsConfig for backward-compat with deprecated flat fields.
	docsEnabled, docsLangs, _ := req.ResolveDocsConfig()
	if docsEnabled && len(docsLangs) == 0 {
		return nil, fmt.Errorf("scriptgeneration: docs.enabled requires at least one language")
	}

	// Create the run BEFORE any external I/O.
	now := time.Now().UTC()
	run := &GenerationRun{
		ID:           "run_" + uuid.New().String(),
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	// Persist the run.
	if err := s.repo.Create(ctx, run); err != nil {
		return nil, fmt.Errorf("scriptgeneration: failed to create run: %w", err)
	}

	// Launch execution asynchronously so the HTTP handler can return
	// 202 Accepted immediately. In production this should be replaced
	// with an outbox event or a durable worker queue.
	// The background context ensures the runner completes even if the
	// HTTP request context is cancelled.
	go s.runner.Execute(context.Background(), run.ID, req)

	return &StartResult{
		Run:       run,
		StatusURL: "/api/v1/script/jobs/" + run.ID + "/full",
	}, nil
}
