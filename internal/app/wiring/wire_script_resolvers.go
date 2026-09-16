package wiring

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	processor "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters/processor"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	research "github.com/Marcuss-ops/PipelineGen/internal/capabilities/research"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	coreasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/embeddings"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	topicsourcecache "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/topicsourcecache"
	"go.uber.org/zap"
)

// readyASSArtifactCounter is the operational half of the sampler's
// subtitle_ready gate: it counts the READY ASS subtitle artifacts of one asset.
//
// It reads asset_subtitle_artifacts, which exists ONLY on the operational SQLite
// store (measured: 0 PostgreSQL tables, no PostgreSQL writer), so it is
// deliberately NOT a media read and must not be re-pointed at the media SSOT
// until that table has a canonical PostgreSQL home. Keeping it a separate port —
// rather than one joined statement — is what let the media half move to
// PostgreSQL without touching this read.
type readyASSArtifactCounter struct {
	db *sql.DB
}

// CountReadyASSArtifacts mirrors the retired correlated subquery exactly:
// format='ass', status='READY', non-empty drive_file_id and drive_url, and
// is_current=1 (an INTEGER flag on SQLite).
func (c readyASSArtifactCounter) CountReadyASSArtifacts(ctx context.Context, assetID string) (int, error) {
	if c.db == nil {
		return 0, fmt.Errorf("ready ASS artifact counter: operational handle is not configured")
	}
	var count int
	err := c.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM asset_subtitle_artifacts
		 WHERE asset_id = ?
		   AND format = 'ass'
		   AND status = 'READY'
		   AND drive_file_id <> ''
		   AND drive_url <> ''
		   AND is_current = 1`, assetID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("ready ASS artifact counter: count for %q: %w", assetID, err)
	}
	return count, nil
}

var _ usecase.ReadyASSArtifactCounter = readyASSArtifactCounter{}

// clipTranscriptEnsurer is the scripts-side adapter over the canonical
// texttracks create-and-persist pipeline.
//
// It is the runtime half of the script-language ↔ clip-association check: when
// a clip bound to a script has no READY transcript in the script's language,
// the resolver asks this adapter to CREATE the missing track and SAVE it in
// the media SSOT, reusing the exact same acquire → translate → save pipeline
// as `asset.text.materialize` and the operator backfill CLI (godlike/06 SSOT:
// one materialization authority, never a second inline translation path).
//
// Fail-closed (godlike/07): the ONLY trustworthy success signal is a re-read
// that finds a READY track. BackfillService reports per-asset failures in its
// result rather than as an error, so a nil error alone is not enough.
type clipTranscriptEnsurer struct {
	clips          texttracks.MediaAssetLister
	reader         scriptports.TextTrackReader
	backfill       clipTextTrackMaterializer
	sourceLanguage string
}

// clipTextTrackMaterializer is the narrow surface the ensurer needs from the
// canonical backfill pipeline. Declaring it here (instead of depending on the
// concrete *texttracks.BackfillService) keeps the adapter unit-testable and
// pins the exact contract it relies on: one call, one per-asset result.
type clipTextTrackMaterializer interface {
	ProcessAsset(ctx context.Context, assetItem *coreasset.Asset, opts texttracks.BackfillOptions) (texttracks.BackfillAssetResult, error)
}

var _ scriptports.ClipTextTrackEnsurer = (*clipTranscriptEnsurer)(nil)

func (e *clipTranscriptEnsurer) EnsureReadyTextTrack(ctx context.Context, assetID, languageCode string, kind detail.TextTrackKind) error {
	if e == nil || e.backfill == nil {
		return fmt.Errorf("clip transcript ensurer: text-track backfill pipeline is not wired")
	}
	if e.clips == nil {
		return fmt.Errorf("clip transcript ensurer: media SSOT asset lister is not wired")
	}
	assetID = strings.TrimSpace(assetID)
	languageCode = strings.TrimSpace(languageCode)
	if assetID == "" || languageCode == "" {
		return fmt.Errorf("clip transcript ensurer: asset_id and language are required")
	}

	// Idempotent fast path: an already-READY track costs nothing. It also keeps
	// a repeated run from re-translating and re-uploading an unchanged track.
	if e.reader != nil {
		if track, _, err := e.reader.FindReady(ctx, assetID, languageCode, kind); err == nil && track != nil {
			return nil
		}
	}

	assets, err := e.clips.List(ctx, coreasset.Filter{IDs: []string{assetID}, Limit: 1})
	if err != nil {
		return fmt.Errorf("clip transcript ensurer: load asset %q: %w", assetID, err)
	}
	if len(assets) == 0 || assets[0] == nil {
		return fmt.Errorf("clip transcript ensurer: asset %q not found in the media SSOT", assetID)
	}

	sourceLanguage := strings.TrimSpace(e.sourceLanguage)
	if sourceLanguage == "" {
		sourceLanguage = languageCode
	}
	if _, err := e.backfill.ProcessAsset(ctx, assets[0], texttracks.BackfillOptions{
		Source:          string(assets[0].Source),
		SourceLanguage:  sourceLanguage,
		TargetLanguages: []string{languageCode},
		TextKind:        kind,
	}); err != nil {
		return fmt.Errorf("clip transcript ensurer: materialize %q/%q: %w", assetID, languageCode, err)
	}

	if e.reader == nil {
		return fmt.Errorf("clip transcript ensurer: text track reader is not wired — cannot verify %q/%q", assetID, languageCode)
	}
	track, _, err := e.reader.FindReady(ctx, assetID, languageCode, kind)
	if err != nil {
		return fmt.Errorf("clip transcript ensurer: verify %q/%q: %w", assetID, languageCode, err)
	}
	if track == nil {
		return fmt.Errorf("clip transcript ensurer: %q has no READY %s track for %q after materialization", assetID, string(kind), languageCode)
	}
	return nil
}

// buildScriptSourceResolvers constructs the canonical source-resolution
// cluster. PostgreSQL + pgvector owns semantic media reads; Qdrant and SQLite
// media mirrors are intentionally not consulted by SourceSearch or Curate.
func buildScriptSourceResolvers(
	cfg *config.Config,
	root *ComposeRoot,
	log *zap.Logger,
) (
	processor.NormalizationConfig,
	*processor.SourceRegistry,
	*usecase.ClipSourceBuilder,
	scriptports.AssetSearchPort,
) {
	gen := root.AI.ScriptGen

	// One sampler implementation is shared by search/catalog/curate.
	//
	// MEDIA-SSOT P2-9 Phase 2: the sampler's subtitle_ready gate reads two
	// facts that live on different engines, so both surfaces are named here
	// instead of being hidden behind one package-global *sql.DB:
	//
	//	media_assets.source            → PostgreSQL media SSOT
	//	asset_subtitle_artifacts       → SQLite (operational only: no PG table)
	//
	// The previous SetSamplerDB(root.DB.DB) handed the gate ONE operational
	// handle for a statement that also read media_assets, so the media half was
	// graded in a database that holds no committed media rows. A closed media
	// plane leaves AssetSource nil and the gate vacuous — the same behaviour the
	// retired nil-SamplerDB check produced.
	samplerDeps := usecase.SamplerGateDeps{}
	if root != nil && root.MediaPostgres != nil {
		samplerDeps.AssetSource = pgmedia.NewMediaAssetSourceReader(root.MediaPostgres)
	}
	if root != nil && root.DB != nil {
		samplerDeps.ReadyASSArtifacts = readyASSArtifactCounter{db: root.DB.DB}
	}
	samplerReg := usecase.NewClipSamplerRegistryWithDeps(samplerDeps)

	var clipSourceBuilder *usecase.ClipSourceBuilder
	if gen != nil {
		if ollamaClient := gen.GetClient(); ollamaClient != nil {
			// MEDIA-SSOT: the script clip resolver reads the PostgreSQL media
			// SSOT whenever it is open; the legacy SQLite ClipsRepository is the
			// documented graceful-degrade fallback only when the media plane is
			// intentionally disabled (root.MediaPostgres == nil). Reading a
			// PostgreSQL-committed asset through SQLite produced not-found
			// hydration for every post-cutover clip.
			if root.MediaPostgres != nil {
				clipSourceBuilder = usecase.NewClipSourceBuilder(pgmedia.NewMediaSearcher(root.MediaPostgres), ollamaClient, log)
			} else {
				clipSourceBuilder = usecase.NewClipSourceBuilder(root.Repos.ClipsRepo, ollamaClient, log)
			}
			if root.AI != nil && root.AI.Reranker != nil && root.AI.Reranker.IsEnabled() {
				clipSourceBuilder.SetReranker(root.AI.Reranker)
			}
			if root.Repos.TextTrackRepo != nil {
				clipSourceBuilder.ConfigureTextTrackReader(root.Repos.TextTrackRepo)
			}
			if root.Repos.SubtitleArtifactRepo != nil {
				clipSourceBuilder.ConfigureSubtitleArtifactRepository(root.Repos.SubtitleArtifactRepo)
			}
			// Script-language ↔ clip-association check: when the media SSOT and
			// the canonical text-track pipeline are both available, a clip whose
			// transcript is missing (or associated with another language) is
			// materialized at runtime — created AND persisted — before the run
			// fails closed. Unwired stays fail-closed (no silent pass).
			if root.TextTracks != nil && root.TextTracks.JobHandler != nil && root.TextTracks.JobHandler.Backfill() != nil && root.MediaPostgres != nil && root.Repos.TextTrackRepo != nil {
				clipSourceBuilder.ConfigureTextTrackEnsurer(&clipTranscriptEnsurer{
					clips:          newPostgresMediaAssetLister(root.MediaPostgres),
					reader:         root.Repos.TextTrackRepo,
					backfill:       root.TextTracks.JobHandler.Backfill(),
					sourceLanguage: ActiveMultilingualConfig(cfg).SourceLanguage,
				})
			}
		}
	}

	normCfg := processor.NormalizationConfig{
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

	sourceReg := processor.NewSourceRegistry(log)
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
	// SourceCatalog resolves against the PostgreSQL media SSOT (P2-9 read
	// migration). The retired *catalog.Repository read the operational SQLite
	// media_assets + clip_search_terms mirror, which the canonical PostgreSQL
	// writer never populates, so the resolver could only grade a stale catalog.
	// A closed media plane leaves SourceCatalog unwired rather than degrading
	// onto that mirror (godlike/07 fail-closed).
	if root.MediaPostgres != nil && clipSourceBuilder != nil {
		catalogPort := &postgresCatalogPort{searcher: pgmedia.NewMediaSearcher(root.MediaPostgres)}
		sourceReg.Register(scriptpkg.SourceCatalog, usecase.NewCatalogSourceResolver(catalogPort, clipSourceBuilder, samplerReg, log))
	}

	// Query vectors use the same E5 contract that populated PostgreSQL
	// media_embeddings. A missing media PostgreSQL handle or E5 sidecar leaves
	// semantic source types unwired rather than falling back to a retired DB.
	var mediaSearcher *pgmedia.MediaSearcher
	var textEmbedder coreasset.Embedder
	if root.MediaPostgres != nil && strings.TrimSpace(cfg.ClipIndexer.ServerURL) != "" {
		mediaSearcher = pgmedia.NewMediaSearcher(root.MediaPostgres)
		textEmbedder = embeddings.NewHTTPTextEmbedderWithTimeout(cfg.ClipIndexer.ServerURL, cfg.ClipIndexer.EmbedTimeout())
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
