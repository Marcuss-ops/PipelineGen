// Package app — build_bundles_ai.go (split July 2026).
//
// This file owns the AI bundle construction. Extracted from
// build_bundles_domain.go per AGENTS.md Pattern 5.
//
// godlike/06 SSOT: BuildAIBundle is the single canonical owner of the
// Ollama + script-gen + translation stack construction.
package app

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	translation "github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/reranker"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
	ytinfra "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/youtube"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// BuildAIBundle constructs the LLM/script/memory stack. Uses Drive.DocClient
// and Drive.DriveUploader (which were constructed earlier).
// PR4.A (June 2026): MemoryRepo is created here (dbs.dualPool.Writer), not in BuildRepoBundle,
// so that the single consumer (startGemmaMemorySweeper) reads it from root.AI
// without going through RepoBundle.
func BuildAIBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, repos *RepoBundle, drive *DriveBundle) (*AIBundle, error) {
	_ = ctx
	_ = drive
	ollamaClient := client.NewClient(cfg.External.OllamaURL, cfg.External.OllamaModel, cfg.External.OllamaTimeoutSeconds)
	ollamaClient.SetNvidiaConfig(cfg.External.UseNvidiaForLLM, cfg.External.NvidiaAPIKey, cfg.External.NvidiaLLMModel)

	// Dedicated embedding client: uses a separate model (nomic-embed-text by
	// default, configurable via OLLAMA_EMBED_MODEL / ollama_embed_model).
	// Ollama returns 500 when a chat model (gemma4:e4b) is used for
	// /api/embeddings — the embed model MUST be an embedding model.
	// Fall back to OllamaModel for backward compat when unset.
	embedModel := cfg.External.OllamaEmbedModel
	if embedModel == "" {
		embedModel = cfg.External.OllamaModel
	}
	ollamaEmbedClient := client.NewClient(cfg.External.OllamaURL, embedModel, cfg.External.OllamaTimeoutSeconds)
	log.Info("embedding client configured",
		zap.String("ollama_url", cfg.External.OllamaURL),
		zap.String("embed_model", embedModel),
	)

	if cfg.External.SearxngURL != "" {
		ws := client.NewWebSearcher(cfg.External.SearxngURL, cfg.External.SearxngMaxResults)
		ollamaClient.SetWebSearcher(ws)
		log.Info("SearXNG web search enabled for LLM context",
			zap.String("searxng_url", cfg.External.SearxngURL),
			zap.Int("max_results", cfg.External.SearxngMaxResults),
		)
	}

	var rerankerClient *reranker.Client
	if cfg.Reranker.Enabled {
		rerankerClient = reranker.NewClient(reranker.Config{
			Enabled:   cfg.Reranker.Enabled,
			URL:       cfg.Reranker.URL,
			Model:     cfg.Reranker.Model,
			TopK:      cfg.Reranker.TopK,
			TimeoutMs: cfg.Reranker.TimeoutMs,
			Weight:    cfg.Reranker.Weight,
		})
		log.Info("reranker client configured",
			zap.Bool("enabled", cfg.Reranker.Enabled),
			zap.String("url", cfg.Reranker.URL),
			zap.String("model", cfg.Reranker.Model),
			zap.Int("top_k", cfg.Reranker.TopK),
		)
	}

	scriptGen := ollama.NewGenerator(ollamaClient)
	translationCache := sqlitescripts.NewCache(dbs.dualPool.Writer)
	scriptGen.SetTranslationCache(translationCache)
	log.Info("translation cache initialized", zap.String("db", dbs.main.Path()))

	// Construct the single application-layer OllamaTranslator per
	// process and route the canonical TranslationPort through it. Wrap
	// the scriptGen (a
	// *ollama.Generator) — the canonical `TranslationCache` is
	// already wired into scriptGen by the SetTranslationCache call
	// above, so its underlying translation call respects the same
	// SQLite-backed cache lookup. The translation logic has one owner in
	// translation.ollama_translator.go.
	ollamaTranslator := translation.NewOllamaTranslator(scriptGen, log)
	log.Info("OllamaTranslator wired", zap.String("port", "translation.TranslationPort"))

	// PR-2 GemmaMemory wiring: construct the real SQLite-backed cache
	// service and inject it into the engine. The engine uses it for
	// CheckGate on the read path; the finalizer uses the same instance
	// for SaveAfterGeneration on the write path. MemoryRepo is retained
	// because root.AI.MemoryRepo is consumed by startBackgroundJobs's
	// gemma-memory-sweeper (internal/app/lifecycle.go:393).
	//
	// The application-layer adapters.Service depends on the typed
	// scriptports.MemoryGate port; the concrete SQLite implementation is
	// provided by sqlitescripts.MemoryRepository and wrapped here in the
	// composition root so that no application-layer package imports
	// database/sql (PR-REFACTOR-P0-IO-BINDER).
	scriptMemRepo := sqlitescripts.NewMemoryRepository(dbs.dualPool.Writer)
	memGate := newScriptMemoryGate(scriptMemRepo)
	memSvc := adapters.NewService(memGate, log)
	engine := usecase.NewEngine(scriptGen, usecase.NewMemoryGateChecker(memSvc), log)

	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5 (July 2026): the
	// WhisperTranscriber adapter is the SOLE canonical concrete
	// for the WhisperTranscriber interface
	// (internal/infrastructure/youtube/ports.go). Constructed
	// here so the AIBundle can expose it for the backfill
	// CLI's 5-priority chain (priority 5: Whisper fallback).
	// The adapter spawns scripts/bridges/whisper_transcriber.py
	// via subprocess and parses its JSON output into the
	// typed asset.TranscriptResult. The concrete instance
	// satisfies BOTH the infrastructure-layer interface
	// AND the application-layer youtubeports.WhisperTranscriberPort
	// (structural subset). DefaultTimeout is left at 0 — the
	// adapter's constructor applies the canonical 5-minute
	// default (see ytinfra.WhisperTranscriberConfig).
	whisperAdapter, wErr := ytinfra.NewWhisperTranscriberAdapter(ytinfra.WhisperTranscriberConfig{}, log)
	if wErr != nil {
		// godlike/07 fail-closed: surface the typed error.
		// The composition root MUST NOT register a
		// half-wired AIBundle — operators see a hard fail
		// at startup, not silent gaps at runtime.
		return nil, fmt.Errorf("compose ai: whisper transcriber: %w", wErr)
	}
	log.Info("WhisperTranscriber adapter configured (Fase 5)")

	// P1 verdetto: SceneTextGenerator wraps the Engine to produce
	// AI-generated scene text (scene-by-scene) separate from the
	// Translator. The adapter bridges scriptgeneration.GenerateRequest
	// to the existing engine ResolvedGenerationPlan.
	sceneTextGen := NewSceneTextGenerator(engine, log)
	log.Info("SceneTextGenerator adapter configured (P1 verdetto)")

	return &AIBundle{
		OllamaClient:       ollamaClient,
		OllamaEmbedClient:  ollamaEmbedClient,
		Reranker:           rerankerClient,
		ScriptGen:          scriptGen,
		OllamaTranslator:   ollamaTranslator,
		MemoryRepo:         memGate,
		MemorySvc:          memSvc,
		ScriptEngine:       engine,
		WhisperTranscriber: whisperAdapter,
		SceneTextGenerator: sceneTextGen,
	}, nil
}
