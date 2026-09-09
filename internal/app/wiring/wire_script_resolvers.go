package wiring

import (
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	research "github.com/Marcuss-ops/PipelineGen/internal/capabilities/research"
	adapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	coreasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/embeddings"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	topicsourcecache "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/topicsourcecache"
	"go.uber.org/zap"
)

// buildScriptSourceResolvers constructs the canonical source-resolution
// cluster. PostgreSQL + pgvector owns semantic media reads; Qdrant and SQLite
// media mirrors are intentionally not consulted by SourceSearch or Curate.
func buildScriptSourceResolvers(
	cfg *config.Config,
	root *ComposeRoot,
	log *zap.Logger,
) (
	adapters.NormalizationConfig,
	*adapters.SourceRegistry,
	*usecase.ClipSourceBuilder,
	scriptports.AssetSearchPort,
) {
	gen := root.AI.ScriptGen

	// One sampler implementation is shared by search/catalog/curate.
	samplerReg := usecase.NewClipSamplerRegistry()
	if root != nil && root.DB != nil {
		usecase.SetSamplerDB(root.DB.DB)
	}

	var clipSourceBuilder *usecase.ClipSourceBuilder
	if gen != nil {
		if ollamaClient := gen.GetClient(); ollamaClient != nil {
			clipSourceBuilder = usecase.NewClipSourceBuilder(root.Repos.ClipsRepo, ollamaClient, log)
			if root.AI != nil && root.AI.Reranker != nil && root.AI.Reranker.IsEnabled() {
				clipSourceBuilder.SetReranker(root.AI.Reranker)
			}
			if root.Repos.TextTrackRepo != nil {
				clipSourceBuilder.ConfigureTextTrackReader(root.Repos.TextTrackRepo)
			}
			if root.Repos.SubtitleArtifactRepo != nil {
				clipSourceBuilder.ConfigureSubtitleArtifactRepository(root.Repos.SubtitleArtifactRepo)
			}
		}
	}

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

	sourceReg := adapters.NewSourceRegistry(log)
	sourceReg.Register(scriptpkg.SourceText, usecase.NewTextSourceResolver())

	if gen != nil && gen.GetClient() != nil && gen.GetClient().WebSearcher() != nil {
		searxngProvider := &searxngWebSearchProviderAdapter{searcher: gen.GetClient().WebSearcher()}
		providers := []scriptports.WebSearchProvider{searxngProvider}
		if cfg.External.ResearchFallbackProvider == "duckduckgo" {
			providers = append(providers, research.NewDuckDuckGoSearchProvider(log))
		}
		multiSearcher := research.NewMultiWebSearcher(log, providers...)
		researchResolver := usecase.NewWebResearchResolver(
			multiSearcher,
			research.NewPageFetcher(time.Duration(cfg.External.WebSearchTimeoutSeconds)*time.Second, 2<<20),
		)
		if err := researchResolver.SetResearchRanker(research.NewResearchRanker(gen.GetClient(), cfg.External.OllamaModel, log)); err != nil {
			panic(fmt.Sprintf("script research ranker: %v", err))
		}
		if err := researchResolver.SetLexicon(linguistics.DefaultLexicon()); err != nil {
			panic(fmt.Sprintf("script research resolver: %v", err))
		}
		if root.CacheDB != nil && root.CacheDB.DB != nil {
			researchResolver.SetCache(topicsourcecache.NewRepository(root.CacheDB.DB))
		}
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
		coordinator := research.NewResearchSearchCoordinator(identityAdapter, plannerAdapter, providers, log)
		coordinator.SetTargetPool(cfg.External.ResearchTargetPoolSize)
		researchResolver.SetSearchCoordinator(usecase.NewResearchCoordinatorAdapter(coordinator))
		researchResolver.SetResearchPolicyVersion(researchPolicyVersion(cfg))
		sourceReg.Register(scriptpkg.SourceResearch, researchResolver)
		log.Info("SourceResearch resolver wired",
			zap.String("research_version", "web-research-v2"),
			zap.Strings("providers", multiSearcher.ProviderNames()),
			zap.Int("target_pool_size", cfg.External.ResearchTargetPoolSize),
		)
	}

	if clipSourceBuilder != nil {
		sourceReg.Register(scriptpkg.SourceClips, usecase.NewClipsSourceResolver(clipSourceBuilder, log))
	}
	if root.Repos.CatalogRepo != nil && clipSourceBuilder != nil {
		catAdapter := &searchCatalogAdapter{catalog: root.Repos.CatalogRepo}
		sourceReg.Register(scriptpkg.SourceCatalog, usecase.NewCatalogSourceResolver(catAdapter, clipSourceBuilder, samplerReg, log))
	}

	// Query vectors use the same E5 contract that populated PostgreSQL
	// media_embeddings. A missing media PostgreSQL handle or E5 sidecar leaves
	// semantic source types unwired rather than falling back to a retired DB.
	var mediaSearcher *pgmedia.MediaSearcher
	var textEmbedder coreasset.Embedder
	if root.MediaPostgres != nil && strings.TrimSpace(cfg.ClipIndexer.ServerURL) != "" {
		mediaSearcher = pgmedia.NewMediaSearcher(root.MediaPostgres)
		textEmbedder = embeddings.NewHTTPTextEmbedder(cfg.ClipIndexer.ServerURL)
	}

	if mediaSearcher != nil && textEmbedder != nil && clipSourceBuilder != nil {
		searchPort := &postgresSemanticSearchPort{
			searcher: mediaSearcher,
			embedder: textEmbedder,
			log:      log,
		}
		sourceReg.Register(scriptpkg.SourceSearch, usecase.NewSearchSourceResolver(searchPort, clipSourceBuilder, samplerReg, log))
		log.Info("SourceSearch resolver wired (PostgreSQL/pgvector + E5 sidecar)")
	}

	var curateResolver *usecase.CurateSourceResolver
	if clipSourceBuilder != nil {
		curateResolver = usecase.NewCurateSourceResolver(clipSourceBuilder, log, samplerReg)
		sourceReg.Register(scriptpkg.SourceCurate, curateResolver)
	}

	var clipSearchPort scriptports.AssetSearchPort
	if mediaSearcher != nil && textEmbedder != nil {
		clipSearchPort = &postgresAssetSearchPort{searcher: mediaSearcher, embedder: textEmbedder}
		log.Info("AssetSearchPort wired for clip catalog (PostgreSQL/pgvector + E5 sidecar)")
	}
	if curateResolver != nil && clipSearchPort != nil {
		curateResolver.SetAssetSearchPort(clipSearchPort)
	}

	return normCfg, sourceReg, clipSourceBuilder, clipSearchPort
}

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
