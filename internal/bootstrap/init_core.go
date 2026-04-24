package bootstrap

import (
	"go.uber.org/zap"
	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/core/entities"
	"velox/go-master/internal/core/job"
	"velox/go-master/internal/core/worker"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/runtime"
	"velox/go-master/pkg/config"
)

// CoreDeps holds the minimal runtime dependencies needed by the stripped-down server.
type CoreDeps struct {
	JobService    *job.Service
	WorkerService *worker.Service
	OllamaClient  *ollama.Client
	ScriptGen     *ollama.Generator
	EntityService *entities.EntityService
	Utility       *common.UtilityHandler
}

// initCore initializes the minimal core stack: Ollama, entities, job/worker state,
// and utility helpers.
func initCore(cfg *config.Config, log *zap.Logger) (*CoreDeps, []runtime.BackgroundService, CleanupFunc, error) {
	core, cleanup, err := initCoreMinimal(cfg, log)
	return core, nil, cleanup, err
}

// initCoreMinimal creates only the services needed by the text/doc server.
func initCoreMinimal(cfg *config.Config, _ *zap.Logger) (*CoreDeps, CleanupFunc, error) {
	ollamaClient := ollama.NewClient(cfg.External.OllamaURL, "")
	scriptGen := ollama.NewGenerator(ollamaClient)

	baseExtractor := entities.NewOllamaExtractor(ollamaClient)
	extractor := entities.NewCachingExtractor(baseExtractor)
	segmenter := entities.NewNLPSegmenter()
	entityService := entities.NewEntityService(extractor, segmenter)

	jobService := job.NewService(nil, cfg)
	workerService := worker.NewService(nil, cfg)

	cleanup := func() {}

	return &CoreDeps{
		JobService:    jobService,
		WorkerService: workerService,
		OllamaClient:  ollamaClient,
		ScriptGen:     scriptGen,
		EntityService: entityService,
		Utility:       common.NewUtilityHandler(),
	}, cleanup, nil
}
