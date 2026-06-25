package app

import (
	"context"

	"go.uber.org/zap"

	scriptcore "github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/types"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
)

// buildYouTubeRuntimeConfig resolves the flat RuntimeConfig consumed by the
// YouTube application layer from the infrastructure *config.Config. All
// nested config paths are flattened here so the application layer has zero
// dependency on `internal/infrastructure/config`.
func buildYouTubeRuntimeConfig(cfg *config.Config) youtubetypes.RuntimeConfig {
	if cfg == nil {
		return youtubetypes.RuntimeConfig{}
	}
	return youtubetypes.RuntimeConfig{
		MaxConcurrentVideoExtracts: cfg.Concurrency.MaxConcurrentVideoExtracts,
		MaxConcurrentOllamaCalls:   cfg.Concurrency.MaxConcurrentOllamaCalls,
		YouTubeExtractTimeout:      cfg.Jobs.YouTubeExtractTimeout,
		DataDir:                    cfg.Storage.DataDir,
		YtdlpPath:                  cfg.External.ResolvedYtdlpPath(),
		ClipsFolderID:              cfg.Drive.ClipsFolder(),
		OllamaModel:                cfg.External.OllamaModel,
		OllamaMetadataModel:        cfg.External.OllamaMetadataModel,
		YouTubeCookiesPath:         cfg.External.YouTubeCookiesPath,
		YouTubeJSRuntimePath:       cfg.External.YouTubeJSRuntimePath,
		YouTubeEnabled:             cfg.Features.YouTubeEnabled,
	}
}

// BuildAIBundle constructs the LLM/script/memory stack. Uses Drive.DocClient
// and Drive.DriveUploader (which were constructed earlier).
// PR4.A (June 2026): MemoryRepo is created here (dbs.main.DB), not in BuildRepoBundle,
// so that the single consumer (startGemmaMemorySweeper) reads it from root.AI
// without going through RepoBundle.
func BuildAIBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, repos *RepoBundle, drive *DriveBundle) (*AIBundle, error) {
	_ = ctx
	_ = drive
	ollamaClient := client.NewClient(cfg.External.OllamaURL, cfg.External.OllamaModel, cfg.External.OllamaTimeoutSeconds)
	ollamaClient.SetNvidiaConfig(cfg.External.UseNvidiaForLLM, cfg.External.NvidiaAPIKey, cfg.External.NvidiaLLMModel)

	if cfg.External.SearxngURL != "" {
		ws := client.NewWebSearcher(cfg.External.SearxngURL, cfg.External.SearxngMaxResults)
		ollamaClient.SetWebSearcher(ws)
		log.Info("SearXNG web search enabled for LLM context",
			zap.String("searxng_url", cfg.External.SearxngURL),
			zap.Int("max_results", cfg.External.SearxngMaxResults),
		)
	}

	scriptGen := ollama.NewGenerator(ollamaClient)
	translationCache := sqlitescripts.NewCache(dbs.main.DB)
	scriptGen.SetTranslationCache(translationCache)
	log.Info("translation cache initialized", zap.String("db", dbs.main.Path()))

	memoryRepo := gemmamemory.NewRepository(dbs.main.DB)
	memorySvc := gemmamemory.NewService(memoryRepo, log)
	log.Info("Gemma Memory Gate service initialized")

	scriptsRepoAdapter := scriptcore.NewRepositoryAdapter(repos.ScriptsRepo)
	engine := scriptcore.NewEngine(scriptGen, memorySvc, scriptsRepoAdapter, log)

	return &AIBundle{
		OllamaClient:  ollamaClient,
		ScriptGen:     scriptGen,
		MemoryRepo:    memoryRepo,
		MemoryService: memorySvc,
		ScriptEngine:  engine,
	}, nil
}
