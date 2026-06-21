// Package batch implements batch script generation orchestration.
//
// This package owns the full batch generation pipeline: outline generation,
// web search, chapter generation, coherence/QA passes, Google Doc creation,
// translation, and DB persistence. It is a use-case layer that depends on
// the domain services (ollama.Generator, Engine, drive.DocClient,
// voiceover.Service, ScriptRepository) but does NOT import HTTP types
// from internal/api/.
package scripts

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// BatchService orchestrates the batch script generation pipeline.
// It owns the full Execute method (formerly ExecuteBatchGeneration on
// ScriptFlowHandler) and all supporting batch phases.
type BatchService struct {
	cfg         *config.Config
	log         *zap.Logger
	generator   *ollama.Generator
	engine      *Engine
	docClient   drive.DocClient
	voService   *voiceover.Service
	scriptsRepo ScriptRepository
}

// NewBatchService creates a new BatchService.
func NewBatchService(
	cfg *config.Config,
	log *zap.Logger,
	gen *ollama.Generator,
	engine *Engine,
	docClient drive.DocClient,
	voSvc *voiceover.Service,
	scriptsRepo ScriptRepository,
) *BatchService {
	return &BatchService{
		cfg:         cfg,
		log:         log,
		generator:   gen,
		engine:      engine,
		docClient:   docClient,
		voService:   voSvc,
		scriptsRepo: scriptsRepo,
	}
}

// Execute runs the full batch generation pipeline synchronously.
// It is the canonical entry point for batch script generation — the HTTP
// handler in api/ delegates to this method.
func (s *BatchService) Execute(ctx context.Context, req *GenerateBatchRequest, onProgress func(int, string)) (BatchGenerateResponse, error) {
	return s.ExecuteBatchGeneration(ctx, req, onProgress)
}
