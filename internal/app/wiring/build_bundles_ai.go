// Package app — build_bundles_ai.go (split July 2026).
//
// This file owns the AI bundle construction. Extracted from
// build_bundles_domain.go per AGENTS.md Pattern 5.
//
// godlike/06 SSOT: BuildAIBundle is the single canonical owner of the
// Ollama + script-gen + translation stack construction.
package wiring

import (
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/models"

	"os"
	"strings"

	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	translation "github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/reranker"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama"
	ollamaadapters "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/scripts"
	ytinfra "github.com/Marcuss-ops/PipelineGen/internal/platform/youtube"
	ytplatform "github.com/Marcuss-ops/PipelineGen/internal/platform/youtube"
)

func whisperBridgeVersion(scriptPath string) string {
	body, err := os.ReadFile(scriptPath)
	if err != nil {
		return "whisper/bridge-v1"
	}
	whisperDigest := digest.SHA256Bytes(body)
	return "whisper/bridge-v1/" + whisperDigest
}

// BuildAIBundle constructs the LLM/script/memory stack. Uses Drive.DocClient
// and Drive.DriveUploader (which were constructed earlier).
// PR4.A (June 2026): MemoryRepo is created here (dbs.DualPool.Writer), not in BuildRepoBundle,
// so that the single consumer (startGemmaMemorySweeper) reads it from root.AI
// without going through RepoBundle.
func BuildAIBundle(ctx context.Context, cfg *config.Config, dbs *Databases, log *zap.Logger, repos *RepoBundle, drive *DriveBundle) (*AIBundle, error) {
	_ = ctx
	_ = drive
	ollamaClient := client.NewClient(cfg.External.OllamaURL, cfg.External.OllamaModel, cfg.External.OllamaTimeoutSeconds)
	ollamaClient.SetNvidiaConfig(cfg.External.UseNvidiaForLLM, cfg.External.NvidiaAPIKey, cfg.External.NvidiaLLMModel)

	// Dedicated embedding client: uses a separate model (the canonical
	// the canonical E5 registry entry by default, configurable via
	// OLLAMA_EMBED_MODEL / ollama_embed_model. Ollama returns 500 when a
	// chat model (gemma4:e4b) is used for /api/embeddings — the embed model
	// MUST be an embedding model. An empty manually-assembled config is
	// resolved to the registry entry rather than falling back to the chat model.
	embedModel := cfg.External.OllamaEmbedModel
	if embedModel == "" {
		embedModel = models.E5.ID
	}
	ollamaEmbedClient := client.NewClient(cfg.External.OllamaURL, embedModel, cfg.External.OllamaTimeoutSeconds)
	log.Info("embedding client configured",
		zap.String("ollama_url", cfg.External.OllamaURL),
		zap.String("embed_model", embedModel),
	)

	if cfg.External.SearxngURL != "" {
		ws := client.NewWebSearcherWithConfig(client.WebSearcherConfig{
			BaseURL: cfg.External.SearxngURL, MaxResults: cfg.External.SearxngMaxResults,
			Timeout:  time.Duration(cfg.External.WebSearchTimeoutSeconds) * time.Second,
			Language: cfg.External.SearxngLanguage, Categories: "general", SafeSearch: 0,
			Engines: strings.Split(cfg.External.SearxngEngines, ","),
		})
		ollamaClient.SetWebSearcher(ws)
		log.Info("SearXNG web search enabled for LLM context",
			zap.String("searxng_url", cfg.External.SearxngURL),
			zap.Int("max_results", cfg.External.SearxngMaxResults),
		)
	}

	var rerankerClient *reranker.Client
	if cfg.Reranker.Enabled {
		var rerankerErr error
		rerankerClient, rerankerErr = reranker.NewValidatedClient(reranker.Config{
			Enabled:   cfg.Reranker.Enabled,
			URL:       cfg.Reranker.URL,
			Model:     cfg.Reranker.Model,
			TopK:      cfg.Reranker.TopK,
			TimeoutMs: cfg.Reranker.TimeoutMs,
			Weight:    cfg.Reranker.Weight,
		})
		if rerankerErr != nil {
			return nil, fmt.Errorf("build AI bundle: reranker configuration: %w", rerankerErr)
		}
		log.Info("reranker client configured",
			zap.Bool("enabled", rerankerClient.IsEnabled()),
			zap.String("url", cfg.Reranker.URL),
			zap.String("model", cfg.Reranker.Model),
			zap.Int("top_k", cfg.Reranker.TopK),
			zap.Float64("weight", cfg.Reranker.Weight),
		)
	}

	scriptGen := ollama.NewGenerator(ollamaClient)
	translationCache := sqlitescripts.NewCache(dbs.DualPool.Writer)
	scriptGen.SetTranslationCache(translationCache)
	log.Info("translation cache initialized", zap.String("db", dbs.Main.Path()))

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
	// gemma-memory-sweeper (internal/app/go:393).
	//
	// The application-layer adapters.Service depends on the typed
	// scriptports.MemoryGate port; the concrete SQLite implementation is
	// provided by sqlitescripts.MemoryRepository and wrapped here in the
	// composition root so that no application-layer package imports
	// database/sql (PR-REFACTOR-P0-IO-BINDER).
	scriptMemRepo := sqlitescripts.NewMemoryRepository(dbs.DualPool.Writer)
	memGate := newScriptMemoryGate(scriptMemRepo)
	memSvc := adapters.NewService(memGate, log)
	engine := usecase.NewEngine(ollamaadapters.NewScriptGeneratorAdapter(scriptGen), usecase.NewMemoryGateChecker(memSvc), log)
	engine.ConfigureScriptDefaults(cfg.Scripts.DefaultLanguage, cfg.Scripts.DefaultTone, cfg.Scripts.Defaults.WordsPerMinute)
	engine.ConfigureSegmentValidation(
		cfg.Scripts.SegmentWordsTolerancePercent,
		cfg.Scripts.TotalWordsTolerancePercent,
		cfg.Scripts.MaxSegmentRegenerationAttempts,
	)

	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5 (July 2026): the
	// WhisperTranscriber adapter is the SOLE canonical concrete
	// for the WhisperTranscriber interface
	// (internal/infrastructure/youtube/ports.go). Constructed
	// here so the AIBundle can expose it for the backfill
	// CLI's 5-priority chain (priority 5: Whisper fallback).
	// The adapter spawns scripts/bridges/whisper_transcriber.py
	// via subprocess and parses its JSON output into the
	// typed detail.TranscriptResult. The concrete instance
	// satisfies BOTH the infrastructure-layer interface
	// AND the application-layer youtubeports.WhisperTranscriberPort
	// (structural subset). DefaultTimeout is left at 0 — the
	// adapter's constructor applies the canonical 5-minute
	// default (see ytinfra.WhisperTranscriberConfig).
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5 (Aug 2026): Whisper is
	// fail-SOFT at composition time. When python3 or the bridge script
	// (scripts/bridges/whisper_transcriber.py) is missing, the adapter
	// returns ErrWhisperBridgeUnavailable — we log a warning and leave
	// the port nil so the 5-priority acquisition chain simply SKIPS
	// the Whisper fallback (priority 5) instead of failing the whole
	// boot. The gap is observable (AcquireService surfaces
	// ErrNoSourceAcquired for clips with no other transcript source),
	// never a silent placeholder transcript (godlike/07 no-fake-
	// availability: nil port = capability absent, not fake data).
	var whisperAdapter ytinfra.WhisperTranscriber
	whisperConcrete, wErr := ytinfra.NewWhisperTranscriberAdapter(ytinfra.WhisperTranscriberConfig{}, log)
	if wErr != nil {
		log.Warn("WhisperTranscriber adapter unavailable; the acquisition chain will skip the Whisper fallback",
			zap.Error(wErr))
	} else {
		log.Info("WhisperTranscriber adapter configured (Fase 5)")
		whisperAdapter = whisperConcrete
		// Derived transcript cache: source bytes + Whisper processor version
		// identify the result; local temporary paths never become cache keys.
		if cache, cacheErr := NewArtifactCache(cfg, dbs.DualPool.Writer, log); cacheErr == nil {
			// The Whisper bridge has its own execution contract; the Ollama
			// chat model is unrelated and must not invalidate or alias
			// transcription artifacts.
			version := whisperBridgeVersion("scripts/bridges/whisper_transcriber.py")
			if cached, wrapErr := ytplatform.NewCachedWhisperTranscriber(whisperAdapter, cache, version, log); wrapErr == nil {
				whisperAdapter = cached
				log.Info("Whisper artifact cache wired", zap.String("processor_version", version))
			} else {
				log.Warn("Whisper artifact cache decorator unavailable", zap.Error(wrapErr))
			}
		} else {
			log.Warn("Whisper artifact cache unavailable; using uncached transcriber", zap.Error(cacheErr))
		}
	}

	// P1 verdetto: SceneTextGenerator wraps the Engine to produce
	// AI-generated scene text (scene-by-scene) separate from the
	// Translator. The adapter bridges scriptgeneration.GenerateRequest
	// to the existing engine ResolvedGenerationPlan.
	sceneTextGen := NewSceneTextGenerator(engine, log)
	sceneTextGen.SetMemoryService(memSvc)
	if repos != nil && repos.ClipsRepo != nil {
		sceneTextGen.SetClipAssetResolver(repos.ClipsRepo)
		log.Info("SceneTextGenerator canonical clip asset resolver configured")
	} else {
		log.Warn("SceneTextGenerator clip asset resolver unavailable; canonical render plans will fail closed")
	}
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
