// Package app — wire_script_resolvers.go.
//
// AZIONE 2 (July 2026): extracted from wire_script.go. This file owns the
// canonical factory for script source resolvers — the single function
// that constructs normCfg, sourceReg (with all 5 resolvers registered),
// clipSourceBuilder, and clipSearchPort. The orchestrator
// (wireScriptFlow) calls this factory, then freezes sourceReg after
// postprocessors are registered, then passes the results to
// buildScriptUseCases in wire_script_usecases.go.
//
// Package boundary: same `package app` as wire_script.go, mirroring
// the wire_script_sources.go / wire_script_curation.go /
// wire_script_postprocess.go / wire_script_adapters.go precedent.
// The factory is a pure-builder — it takes composition-root
// dependencies and constructs typed-capability objects with zero
// side effects (no freeze, no validation, no registration with
// external services).
//
// Cross-references:
//   - internal/app/wire_script.go: the caller (wireScriptFlow
//     invokes buildScriptSourceResolvers, then freezes + validates).
//   - internal/app/wire_script_usecases.go: the sibling factory
//     that consumes normCfg, sourceReg, clipSourceBuilder,
//     clipSearchPort (AZIONE 2 companion file).
//   - internal/app/wire_script_sources.go: adapter types used
//     inline (qdrantSemanticSearchPort, clipSearchPortAdapter).
//   - internal/application/scripts/adapters: SourceRegistry,
//     NormalizationConfig (the canonical types this factory builds).
//   - internal/application/scripts/usecase: source-resolver
//     constructors (NewTextSourceResolver, NewClipsSourceResolver,
//     NewCatalogSourceResolver, NewSearchSourceResolver,
//     NewCurateSourceResolver, ClipSourceBuilder).
//   - internal/infrastructure/qdrant: Searcher, TextEmbedder,
//     NewClipSearchAdapter.
//   - internal/infrastructure/embeddings: OllamaEmbedderAdapter.
package app

import (
	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/reranker"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/embeddings"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// buildScriptSourceResolvers constructs the source-resolution
// capability cluster: normalization config, the frozen-ready
// source registry (5 resolvers: Text, Clips, Catalog, Search,
// Curate), the ClipSourceBuilder shared by resolvers and
// mediaCurator, and the Qdrant-backed ClipSearchPort consumed
// by the curate resolver and mediaCurator.
//
// The returned sourceReg is NOT frozen — the orchestrator
// (wireScriptFlow) freezes it after postprocessor registration
// to preserve the canonical ordering: resolvers → postprocessors
// → freeze → validate.
//
// Nil-tolerance: every resolver registration is gated on its
// required infrastructure dependency (ClipsRepo, CatalogRepo,
// QdrantSearcher, ollamaClient). When a dep is absent, the
// corresponding resolver is silently skipped — the composition-
// time validator in wire_script_adapters.go surfaces the gap
// after freeze.
func buildScriptSourceResolvers(
	cfg *config.Config,
	root *ComposeRoot,
	log *zap.Logger,
) (
	adapters.NormalizationConfig,
	*adapters.SourceRegistry,
	*usecase.ClipSourceBuilder,
	scriptports.ClipSearchPort,
) {
	gen := root.AI.ScriptGen

	// FASE-7 move-only refactor (July 2026): ClipSampler is the
	// SINGLE shared selection port for search/catalog/curate
	// resolvers. The registry holds one impl; SamplerFor(caller)
	// returns the same instance for every resolver with a
	// caller-tagged audit context. godlike/06 SSOT forbids per-
	// resolver sampler impls.
	samplerReg := usecase.NewClipSamplerRegistry()

	// ── Clip source builder ────────────────────────────────────────
	var clipSourceBuilder *usecase.ClipSourceBuilder
	if gen != nil {
		if ollamaClient := gen.GetClient(); ollamaClient != nil {
			clipSourceBuilder = usecase.NewClipSourceBuilder(root.Repos.ClipsRepo, ollamaClient, log)
			if cfg.Reranker.Enabled {
				clipSourceBuilder.SetReranker(reranker.NewClient(reranker.Config{
					Enabled:   cfg.Reranker.Enabled,
					URL:       cfg.Reranker.URL,
					Model:     cfg.Reranker.Model,
					TopK:      cfg.Reranker.TopK,
					TimeoutMs: cfg.Reranker.TimeoutMs,
				}))
			}
			// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026):
			// cut the video pipeline over to TextTrackReader.
			// `root.Repos.TextTrackRepo` is the canonical
			// *TextTrackRepositorySQLite wired in the
			// composition root; the sub-interface
			// (scriptports.TextTrackReader) is the typed
			// surface consumed by ClipSourceBuilder.
			//
			// Fase 4 strict cutover: there is no legacy
			// metadata_json[\"transcript\"] fallback; the
			// TextTrackReader is the SOLE read surface. The
			// removed (see internal/platform/config/media.go).
			if root.Repos.TextTrackRepo != nil {
				clipSourceBuilder.ConfigureTextTrackReader(root.Repos.TextTrackRepo)
			}
		}
	} // ── Normalization config ───────────────────────────────────────
	normCfg := adapters.NormalizationConfig{
		DefaultLanguage:            cfg.Scripts.DefaultLanguage,
		DefaultTone:                cfg.Scripts.DefaultTone,
		DefaultDurationSeconds:     cfg.Scripts.DefaultDurationSeconds,
		OllamaModel:                cfg.External.OllamaModel,
		ChannelID:                  cfg.Scripts.ChannelID,
		MinWordFloor:               cfg.Scripts.MinWordFloor,
		PromptVersion:              "v1",
		EditorPromptVersion:        "v1",
		QAPromptVersion:            "v1",
		DefaultSentencesPerImage:   10,
		DefaultImagesPerScene:      2,
		MaxBatchWorkers:            cfg.Scripts.MaxBatchWorkers,
		LogSourceTextPreview:       cfg.Scripts.LogSourceTextPreview,
		SourceTextPreviewChars:     cfg.Scripts.SourceTextPreviewChars,
		WordsPerSecondClipEvidence: cfg.Scripts.WordsPerSecondClipEvidence,
	}

	// ── Source registry (5 resolvers) ──────────────────────────────
	sourceReg := adapters.NewSourceRegistry(log)

	// Text resolver — always available (no external dep).
	sourceReg.Register(scriptpkg.SourceText, usecase.NewTextSourceResolver())

	// Clips resolver — gated on clipSourceBuilder (requires ollamaClient).
	if clipSourceBuilder != nil {
		sourceReg.Register(scriptpkg.SourceClips, usecase.NewClipsSourceResolver(clipSourceBuilder, log))
	}

	// Catalog resolver — gated on CatalogRepo + clipSourceBuilder.
	// Reuses searchCatalogAdapter (assets_adapters.go) to bridge
	// *catalog.Repository → search.LocalCatalogPort.
	if root.Repos.CatalogRepo != nil && clipSourceBuilder != nil {
		catAdapter := &searchCatalogAdapter{catalog: root.Repos.CatalogRepo}
		sourceReg.Register(scriptpkg.SourceCatalog, usecase.NewCatalogSourceResolver(catAdapter, clipSourceBuilder, samplerReg, log))
	}

	// ── Qdrant embedder (shared by SemanticSearchPort and ClipSearchPort) ──
	var ollamaEmbedder search.TextEmbedder
	if root.Process != nil && root.Process.QdrantSearcher != nil && root.AI != nil {
		if ollamaClient := root.AI.OllamaEmbedClient; ollamaClient != nil {
			ollamaEmbedder = search.NewTextEmbedderAdapter(embeddings.NewOllamaEmbedderAdapter(ollamaClient))
		}
	}

	// Search resolver — gated on QdrantSearcher + ollamaEmbedder + clipSourceBuilder.
	if root.Process != nil && root.Process.QdrantSearcher != nil && ollamaEmbedder != nil && clipSourceBuilder != nil {
		searchPort := &qdrantSemanticSearchPort{
			searcher:   root.Process.QdrantSearcher,
			embedder:   ollamaEmbedder,
			vectorName: "text",
			log:        log,
		}
		sourceReg.Register(scriptpkg.SourceSearch, usecase.NewSearchSourceResolver(searchPort, clipSourceBuilder, samplerReg, log))
		log.Info("SourceSearch resolver wired (Qdrant + Ollama embedder)")
	}

	// Curate resolver — gated on clipSourceBuilder.
	var curateResolver *usecase.CurateSourceResolver
	if clipSourceBuilder != nil {
		curateResolver = usecase.NewCurateSourceResolver(clipSourceBuilder, log, samplerReg)
		sourceReg.Register(scriptpkg.SourceCurate, curateResolver)
	}

	// ── ClipSearchPort (Qdrant) ────────────────────────────────────
	var clipSearchPort scriptports.ClipSearchPort
	if root.Process != nil && root.Process.QdrantSearcher != nil && ollamaEmbedder != nil {
		clipSearchPort = search.NewClipSearchAdapter(root.Process.QdrantSearcher, ollamaEmbedder, "text", log)
		log.Info("ClipSearchPort wired (Qdrant + Ollama embedder)")
	}

	// Wire ClipSearchPort to curate resolver (via composition-root bridge).
	if curateResolver != nil && clipSearchPort != nil {
		curateResolver.SetClipSearchPort(&clipSearchPortAdapter{port: clipSearchPort})
	}

	return normCfg, sourceReg, clipSourceBuilder, clipSearchPort
}
