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
//   - internal/platform/qdrant: Searcher, TextEmbedder,
//     NewClipSearchAdapter.
//   - internal/platform/embeddings: OllamaEmbedderAdapter.
package app

import "time"

import (
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	research "github.com/Marcuss-ops/PipelineGen/internal/capabilities/research"
	adapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/reranker"
	topicsourcecache "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/topicsourcecache"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/embeddings"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/search"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/research"
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
	root *wiring.ComposeRoot,
	log *zap.Logger,
) (
	adapters.NormalizationConfig,
	*adapters.SourceRegistry,
	*usecase.ClipSourceBuilder,
	scriptports.AssetSearchPort,
) {
	gen := root.AI.ScriptGen

	// FASE-7 move-only refactor (July 2026): ClipSampler is the
	// SINGLE shared selection port for search/catalog/curate
	// resolvers. The registry holds one impl; SamplerFor(caller)
	// returns the same instance for every resolver with a
	// caller-tagged audit context. godlike/06 SSOT forbids per-
	// resolver sampler impls.
	samplerReg := usecase.NewClipSamplerRegistry()
	if root != nil && root.DB != nil {
		usecase.SetSamplerDB(root.DB.DB)
	}

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
			if root.Repos.SubtitleArtifactRepo != nil {
				clipSourceBuilder.ConfigureSubtitleArtifactRepository(root.Repos.SubtitleArtifactRepo)
			}
		}
	} // ── Normalization config ───────────────────────────────────────
	normCfg := adapters.NormalizationConfig{
		DefaultLanguage:            cfg.Scripts.DefaultLanguage,
		DefaultTone:                cfg.Scripts.DefaultTone,
		WordsPerMinute:             cfg.Scripts.Defaults.WordsPerMinute,
		SafetyLanguage:             cfg.Scripts.Defaults.SafetyLanguage,
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
		ScriptDocsFolderID:         cfg.Scripts.ScriptDocsFolderID,
	}

	// ── Source registry (5 resolvers) ──────────────────────────────
	sourceReg := adapters.NewSourceRegistry(log)

	// Text resolver — always available (no external dep).
	sourceReg.Register(scriptpkg.SourceText, usecase.NewTextSourceResolver())

	// Research resolver — the only source resolver that can navigate the
	// public web. It is registered only when SearXNG is wired; absence is
	// intentionally fail-closed for source.type=research.
	if gen != nil && gen.GetClient() != nil && gen.GetClient().WebSearcher() != nil {
		// Build the multi-provider searcher: SearXNG (primary) + optional DDG fallback.
		searxngProvider := &searxngWebSearchProviderAdapter{searcher: gen.GetClient().WebSearcher()}
		var providers []scriptports.WebSearchProvider
		providers = append(providers, searxngProvider)
		if cfg.External.ResearchFallbackProvider == "duckduckgo" {
			providers = append(providers, webresearch.NewDuckDuckGoSearchProvider(log))
		}
		multiSearcher := webresearch.NewMultiWebSearcher(log, providers...)

		researchResolver := usecase.NewWebResearchResolver(
			multiSearcher,
			webresearch.NewPageFetcher(time.Duration(cfg.External.WebSearchTimeoutSeconds)*time.Second, 2<<20),
		)
		if err := researchResolver.SetResearchRanker(research.NewResearchRanker(gen.GetClient(), cfg.External.OllamaModel, log)); err != nil {
			panic(fmt.Sprintf("script research ranker: %v", err))
		}
		if err := researchResolver.SetLexicon(linguistics.DefaultLexicon()); err != nil {
			panic(fmt.Sprintf("script research resolver: %v", err))
		}
		if root.DB != nil {
			researchResolver.SetCache(topicsourcecache.NewRepository(root.DB.DB))
		}

		// Wire the subject-aware search coordinator.
		identityAdapter := &research.SubjectIdentityAdapter{
			Resolve: func(subject string) scriptpkg.SubjectIdentity {
				return usecase.NewSubjectIdentityResolver().Resolve(subject)
			},
		}
		plannerAdapter := &research.QueryPlannerAdapter{
			FullPlan: func(identity scriptpkg.SubjectIdentity, maxQueries int) []string {
				return usecase.NewQueryPlanner().FullPlan(identity, maxQueries)
			},
		}
		// The coordinator drives the provider registry directly (subject-aware
		// escalation per provider); the MultiWebSearcher stays wired to the
		// resolver as its dumb merge/dedup searcher for the fallback path.
		coordinator := research.NewResearchSearchCoordinator(identityAdapter, plannerAdapter, providers, log)
		coordinator.SetTargetPool(cfg.External.ResearchTargetPoolSize)
		researchResolver.SetSearchCoordinator(&researchCoordinatorAdapter{coordinator: coordinator})
		// Fold the provider policy into the research cache fingerprint so
		// SearXNG-only and SearXNG+DDG deployments never share cache entries.
		researchResolver.SetResearchPolicyVersion(researchPolicyVersion(cfg))

		sourceReg.Register(scriptpkg.SourceResearch, researchResolver)
		log.Info("SourceResearch resolver wired",
			zap.String("research_version", "web-research-v2"),
			zap.Strings("providers", multiSearcher.ProviderNames()),
			zap.Int("target_pool_size", cfg.External.ResearchTargetPoolSize),
		)
	}

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
	// Query vectors must come from the same E5 sidecar contract as the
	// indexed document vectors. Ollama is a chat/legacy embedder and must
	// not silently create a second vector space (mirrors
	// registerInternalModules).
	var textEmbedder search.TextEmbedder
	if root.Process != nil && root.Process.QdrantSearcher != nil && cfg.ClipIndexer.ServerURL != "" {
		textEmbedder = search.NewTextEmbedderAdapter(embeddings.NewHTTPTextEmbedder(cfg.ClipIndexer.ServerURL))
	}

	// Search resolver — gated on QdrantSearcher + textEmbedder + clipSourceBuilder.
	if root.Process != nil && root.Process.QdrantSearcher != nil && textEmbedder != nil && clipSourceBuilder != nil {
		searchPort := &qdrantSemanticSearchPort{
			searcher:   root.Process.QdrantSearcher,
			embedder:   textEmbedder,
			hydrator:   root.Repos.ClipsRepo,
			vectorName: "text",
			log:        log,
		}
		sourceReg.Register(scriptpkg.SourceSearch, usecase.NewSearchSourceResolver(searchPort, clipSourceBuilder, samplerReg, log))
		log.Info("SourceSearch resolver wired (Qdrant + E5 sidecar embedder)")
	}

	// Curate resolver — gated on clipSourceBuilder.
	var curateResolver *usecase.CurateSourceResolver
	if clipSourceBuilder != nil {
		curateResolver = usecase.NewCurateSourceResolver(clipSourceBuilder, log, samplerReg)
		sourceReg.Register(scriptpkg.SourceCurate, curateResolver)
	}

	// ── canonical AssetSearchPort (Qdrant) ───────────────────────
	var clipSearchPort scriptports.AssetSearchPort
	if root.Process != nil && root.Process.QdrantSearcher != nil && textEmbedder != nil {
		clipSearchPort = search.NewSemanticAssetSearchAdapter(root.Process.QdrantSearcher, textEmbedder, "text", search.KindClip, log)
		log.Info("AssetSearchPort wired for clip catalog (Qdrant + E5 sidecar embedder)")
	}

	// Wire ClipSearchPort to curate resolver (via composition-root bridge).
	if curateResolver != nil && clipSearchPort != nil {
		curateResolver.SetAssetSearchPort(clipSearchPort)
	}

	return normCfg, sourceReg, clipSourceBuilder, clipSearchPort
}

// researchPolicyVersion derives the opaque provider-policy token folded
// into the research cache fingerprint (per-topic and aggregate keys). It
// must be identical for the submission preflight and the worker resolver
// — both are wired from the same config — so SearXNG-only and
// SearXNG+DDG deployments never share cache entries, and a target-pool
// change invalidates previously cached research.
func researchPolicyVersion(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	provider := strings.TrimSpace(cfg.External.ResearchFallbackProvider)
	if provider == "" {
		provider = "searxng"
	}
	targetPool := cfg.External.ResearchTargetPoolSize
	if targetPool <= 0 {
		targetPool = 8
	}
	return fmt.Sprintf("provider=%s,target_pool=%d", provider, targetPool)
}
