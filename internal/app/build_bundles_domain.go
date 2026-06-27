package app

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/videomuscles"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	lessonsSvc "github.com/Marcuss-ops/PipelineGen/internal/application/lessons"
	scriptcore "github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"

	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	voiceoversync "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/sync"
	"github.com/Marcuss-ops/PipelineGen/internal/application/youtube"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/types"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/autotag"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	pkgffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	ytinfra "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/youtube"
	ytcache "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/youtube/cache"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"github.com/Marcuss-ops/PipelineGen/pkg/portutil"
)

// BuildDomainBundle builds the media-domain services.
//
// PR-12d (June 2026): takes the OutboxBundle as the LAST positional
// argument so the canonical outbox.Dispatcher is available for
// constructor injection into images.Service. NewComposition must
// call BuildOutboxBundle BEFORE BuildDomainBundle for this dep to
// be satisfied (see composition.go::NewComposition).
func BuildDomainBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, drive *DriveBundle, repos *RepoBundle, search *SearchBundle, process *ProcessBundle, ai *AIBundle, outbox *OutboxBundle) (*DomainBundle, error) {
	// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): construct
	// the canonical mutations.AssetMutationDispatcher SSOT once here so
	// the buildDomainBundle-level NewClipsRegistry call routes media_assets
	// UPSERT through the same outbox+tx writer (QDRANT-002 atomicity
	// invariant). The same SSOT flows into buildIngestService (1 caller
	// below) and into the rest of the composition graph via outbox.
	// Fail-closed: a nil outbox.Dispatcher is surfaced as a BundleError so
	// WireRegistry aborts before any half-built bundle is cached.
	var mutationsDisp mutations.AssetMutationDispatcher
	if outbox != nil && outbox.Dispatcher != nil {
		var err error
		mutationsDisp, err = newMutationsDispatcherAdapter(outbox.Dispatcher)
		if err != nil {
			return nil, fmt.Errorf("compose domains: %w", err)
		}
	} else {
		return nil, fmt.Errorf("compose domains: outbox.Dispatcher is required — QDRANT-002 PR7 removed the legacy fallback; root.Outbox must be built first")
	}
	clipsRegistry := artifacts.NewClipsRegistry(
		dbs.main.DB,
		repos.Assets.Repository(),
		repos.Assets,
		repos.Assets.LocationRepository(),
		repos.Assets.ProcessingRepository(),
		mutationsDisp,
	)
	youtubeLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    clipsRegistry,
		DriveClient: drive.DriveClient,
		AssetIndex:  search.AssetIndexService,
	}, log)

	voMetaWriter := semantic.NewMetadataWriter(
		cfg.Paths.PythonScriptsDir,
		cfg.Storage.TempPath(),
		cfg.External.OllamaURL,
		cfg.External.OllamaModel,
		log,
	)

	clipProcessor := pkgffmpeg.NewFromConfig(cfg)
	videoPipeline := videomuscles.NewPipeline(cfg, log, clipProcessor)
	videoPipelineAdapter := ytinfra.NewVideoPipelineAdapter(videoPipeline)

	folderMemSvc := foldermemory.NewService(log, repos.ClipsRepo)
	metaFetcher := ytinfra.NewMetadataFetcherAdapter(cfg, nil)
	driveFolderMgr := newDriveFolderMgrAdapter(drive.DriveUploader, log)
	youtubeCache := ytcache.NewService(ytcache.Deps{DB: repos.ClipsRepo.DB(), Log: log})

	var clipIndexerAdapterValue youtubeports.ClipIndexerPort
	if process.ClipIndexerService != nil {
		clipIndexerAdapterValue = &clipIndexerAdapter{inner: process.ClipIndexerService}
	}

	searchRunnerAdapter := ytinfra.NewSearchRunnerAdapter(cfg, log)
	if searchRunnerAdapter == nil {
		return nil, fmt.Errorf("compose domains: youtube SearchRunnerPort nil (cfg or log missing — fail-closed per PR2)")
	}
	if portutil.IsNilPort(searchRunnerAdapter) {
		return nil, fmt.Errorf("compose domains: youtube SearchRunnerPort typed-nil (portutil.IsNilPort true — fail-closed per PR2)")
	}

	youtubeDeps := youtube.ServiceDeps{
		Cfg:               buildYouTubeRuntimeConfig(cfg),
		Log:               log,
		MediaProcessor:    process.MediaProcessor,
		VideoPipeline:     videoPipelineAdapter,
		LifecycleService:  youtubeLifecycle,
		AssetDestResolver: drive.DestResolver,
		AssetRepo:         repos.Assets.Repository(),
		Clips:             newClipStoreAdapter(repos.ClipsRepo),
		Cache:             youtubeCache,
		Monitors:          newMonitorsStoreAdapter(repos.MonitorsRepo),
		Indexer:           clipIndexerAdapterValue,
		Ollama:            ai.OllamaClient,
		MetaFetcher:       metaFetcher,
		DriveFolderMgr:    driveFolderMgr,
		FolderMemory:      newFolderMemoryAdapter(folderMemSvc),
		SearchRunner:      searchRunnerAdapter,
	}
	if err := youtube.ValidateServiceDeps(youtubeDeps); err != nil {
		return nil, fmt.Errorf("compose youtube: %w", err)
	}
	youtubeClipService := youtube.NewService(youtubeDeps)

	voiceoverSvc, voiceoverRepo := buildVoiceoverService(ctx, cfg, dbs, log,
		drive.DriveClient, drive.DriveUploader,
		search.AssetIndexService, process.ClipIndexerService,
		drive.DestResolver,
		voMetaWriter, ai.ScriptGen,
	)

	booksSvc := buildBooksService(cfg, dbs, log, drive.DriveUploader, voiceoverSvc)

	ingestSvc := buildIngestService(cfg, log, dbs, drive.DriveClient, repos, search, mutationsDisp)

	imageSvc, metaWriter := buildImagesService(ctx, cfg, log,
		drive.DriveClient, repos.ClipsRepo, repos.ClipsRepo,
		drive.StyleRegistry, ai.ScriptGen,
		drive.MediaStore, repos.ImageRepo,
		voMetaWriter, ingestSvc,
		outbox.Dispatcher,
	)

	// Wave 15 (June 2026): RealtimeService split into two typed ports —
	// see composition.go DomainBundle for rationale. Both stay typed-nil
	// (package removed in commit d61068b3).
	var realtimeMatcher assetsapi.RealtimeMatcher
	var realtimeSearch scriptcore.RealtimeSearchService

	// autotagVectorStore removed during the bundle simplification
	// deleted. The autotag service no longer takes a vector-store indexer;
	// its semantic-tagging pipeline can still consume clip embeddings from
	// the DB but no longer propagates them to a vector store backend.
	autotagSvc := autotag.NewService(dbs.main.DB, repos.Assets.Repository(), process.VLMClient, nil, log)

	// Wave 15 (June 2026): typed-nil for scriptcore.AssocSearchService —
	// replaces the preceding `interface{}` carrier.
	var assocService scriptcore.AssocSearchService
	log.Info("association service unavailable — package removed from remote")

	lessonsS := lessonsSvc.NewService(
		&lessonsSvc.LessonsConfig{
			Enabled:             cfg.Lessons.Enabled,
			DefaultModel:        cfg.Lessons.DefaultModel,
			DefaultTone:         cfg.Lessons.DefaultTone,
			DefaultLanguage:     cfg.Lessons.DefaultLanguage,
			DefaultImageModel:   cfg.Lessons.DefaultImageModel,
			MaxParallelChapters: cfg.Lessons.MaxParallelChapters,
			OllamaURL:           cfg.External.OllamaURL,
		},
		ai.ScriptGen, imageSvc, drive.DocClient, log,
	)
	log.Info("Lessons service initialized", zap.Bool("enabled", cfg.Lessons.Enabled))

	var vosyncSvc *voiceoversync.Service
	if voFolder := cfg.Drive.VoiceoverFolder(); voFolder != "" && voiceoverRepo != nil {
		vosyncSvc = voiceoversync.NewService(drive.DriveUploader, voiceoverRepo, search.AssetTreeService, voFolder, log)
		log.Info("Voiceover sync service initialized", zap.String("root_folder_id", voFolder))
	}

	return &DomainBundle{
		YoutubeClipService: youtubeClipService,
		VoiceoverService:   voiceoverSvc,
		VoiceoverSync:      vosyncSvc,
		ImageService:       imageSvc,
		IngestService:      ingestSvc,
		BooksService:       booksSvc,
		LessonsService:     lessonsS,
		MetaWriter:         metaWriter,
		RealtimeMatcher:    realtimeMatcher,
		RealtimeSearch:     realtimeSearch,
		AutotagService:     autotagSvc,
		AssocService:       assocService,
	}, nil
}

// buildIngestService constructs the ingest.Service from the same deps
// that WireMediaIngest uses.
//
// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): outbox and
// mutationsDisp are the 7th and 8th positional args so the four
// artifacts.NewClipsRegistry + ingest.NewClipStoreAdapter ctor calls
// inside this function route their media_assets UPSERT through the
// canonical outbox+tx writer. mutationsDisp is constructed once in
// BuildDomainBundle and reused so the same SSOT instance flows into
// every caller without raising the boot-time cost of repeated
// newMutationsDispatcherAdapter wraps.
//
// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): mutationsDisp
// is the 7th positional arg so the four
// artifacts.NewClipsRegistry + ingest.NewClipStoreAdapter ctor calls
// inside this function route their media_assets UPSERT through the
// canonical outbox+tx writer. mutationsDisp is constructed once in
// BuildDomainBundle and reused so the same SSOT instance flows into
// every caller without raising the boot-time cost of repeated
// newMutationsDispatcherAdapter wraps. The buildIngestService signature
// drops the previously-threaded *OutboxBundle arg (it was never read
// inside the function body) — the caller still constructs mutationsDisp
// from outbox.Dispatcher, so the Site-1 wiring is unchanged.
func buildIngestService(cfg *config.Config, log *zap.Logger, dbs *databases, driveClient *gdrive.Service, repos *RepoBundle, search *SearchBundle, mutationsDisp mutations.AssetMutationDispatcher) *ingest.Service {
	if driveClient == nil {
		return nil
	}
	if repos.ImageRepo == nil || repos.VoiceoverRepo == nil || repos.ClipsRepo == nil || search.AssetIndexService == nil {
		return nil
	}
	if mutationsDisp == nil {
		log.Warn("buildIngestService: mutationsDisp is nil — ingest will surface ErrDispatcherUnavailable on first Upsert (QDRANT-002 PR7 fail-closed)")
	}
	imagesRegistry := imgservice.NewRegistryAdapter(repos.ImageRepo, cfg.Storage.ImagesPath(), log)
	imagesLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: imagesRegistry, DriveClient: driveClient, AssetIndex: search.AssetIndexService, Store: ingest.NewImageStoreAdapter(repos.ImageRepo, cfg.Storage.ImagesPath())}, log)
	voiceoverRegistry := voiceover.NewVoiceoverRegistryAdapter(repos.VoiceoverRepo)
	voiceoverLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: voiceoverRegistry, DriveClient: driveClient, AssetIndex: search.AssetIndexService, Store: ingest.NewVoiceoverStoreAdapter(repos.VoiceoverRepo)}, log)
	clipRegistry := artifacts.NewClipsRegistry(dbs.main.DB, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(), mutationsDisp)
	clipLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: clipRegistry, DriveClient: driveClient, AssetIndex: search.AssetIndexService, Store: ingest.NewClipStoreAdapter(dbs.main.DB, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(), mutationsDisp)}, log)
	stockRegistry := artifacts.NewClipsRegistry(dbs.main.DB, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(), mutationsDisp)
	stockLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: stockRegistry, DriveClient: driveClient, AssetIndex: search.AssetIndexService, Store: ingest.NewClipStoreAdapter(dbs.main.DB, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(), mutationsDisp)}, log)
	return ingest.NewService(cfg, log, driveClient, map[ingest.Kind]*ingest.Pipeline{
		ingest.KindImage:     {Kind: ingest.KindImage, DefaultSource: "image", RootFolderID: cfg.Drive.ImagesFolder(), Lifecycle: imagesLifecycle},
		ingest.KindVoiceover: {Kind: ingest.KindVoiceover, DefaultSource: "voiceover", RootFolderID: cfg.Drive.VoiceoverFolder(), Lifecycle: voiceoverLifecycle},
		ingest.KindClip:      {Kind: ingest.KindClip, DefaultSource: "youtube", RootFolderID: cfg.Drive.ClipsFolder(), Lifecycle: clipLifecycle},
		ingest.KindStock:     {Kind: ingest.KindStock, DefaultSource: "stock", RootFolderID: cfg.Drive.StockFolder(), Lifecycle: stockLifecycle},
	})
}

// buildYouTubeRuntimeConfig resolves the flat RuntimeConfig consumed by the
// YouTube application layer from the infrastructure *config.Config. All
// nested config paths are flattened here so the application layer has zero
// dependency on `internal/platform/config`.
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

	// PR 5 (June 2026): NewEngine no longer takes a ScriptRepository
	// arg — engine persistence is gone; the single-writer is
	// PersistenceProcessor. RepositoryAdapter is still constructed
	// here because wireScriptFlow(BuildRepoBundle) uses it for
	// PersistenceProcessor registration (see wire_script.go).
	engine := scriptcore.NewEngine(scriptGen, memorySvc, log)

	return &AIBundle{
		OllamaClient:  ollamaClient,
		ScriptGen:     scriptGen,
		MemoryRepo:    memoryRepo,
		MemoryService: memorySvc,
		ScriptEngine:  engine,
	}, nil
}

// BuildMaintBundle constructs the periodic maintenance + deletion services.
func BuildMaintBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, drive *DriveBundle, repos *RepoBundle, search *SearchBundle, jobs *JobsBundle, outboxBundle *OutboxBundle) (*MaintBundle, error) {
	_ = ctx
	deletionSvc := deletion.NewDeletionService(
		repos.ClipsRepo, repos.ClipsRepo, repos.ClipsRepo,
		repos.VoiceoverRepo, repos.ImageRepo,
		drive.DriveUploader, search.AssetTreeService, search.AssetIndexService,
		outboxBundle.Dispatcher,
		log,
	)
	maintenanceSvc := maintenance.NewService(cfg, log,
		search.AssetIndexService, search.AssetTreeService, deletionSvc,
		jobs.Service, dbs.main.DB,
	)
	// Registries-and-SSOT (June 2026): this is the canonical site for
	// the `system.cleanup` job-type registration. Spec §"Uniqueness"
	// requires composition to fail on duplicate job types; the previous
	// log-Warn-and-continue pattern silently absorbed any second-call
	// attempt (a latent bug that manifested after WireRegistry's
	// duplicate call was removed). Propagate so any future second-call
	// path fails composition rather than masking the underlying
	// Dispatcher error.
	if err := maintenanceSvc.RegisterHandler(); err != nil {
		return nil, fmt.Errorf("compose: register maintenance job handler (BuildMaintBundle): %w", err)
	}

	return &MaintBundle{
		MaintenanceSvc: maintenanceSvc,
		DeletionSvc:    deletionSvc,
	}, nil
}

// BuildSyncBundle constructs ONLY the catalog→Drive sync. ProviderRegistry
// moved to BuildSearchBundle (PR4 review).
//
// PR-D (June 2026): catalogsync.NewService now takes Deps{} + returns
// (*Service, error). The legacy late-bind SetDispatcher call is gone;
// the dispatcher is captured at construction time. Composition-root
// pre-rejection lives here so a nil outbox dispatcher fails the bundle
// build with an explicit error instead of racing the late-bind sequence.
func BuildSyncBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, repos *RepoBundle, search *SearchBundle, process *ProcessBundle, drive *DriveBundle, outbox *OutboxBundle) (*SyncBundle, error) {
	_ = ctx
	_ = cfg
	_ = dbs
	_ = repos
	syncTargets := buildSyncTargets(cfg, repos.ClipsRepo, repos.ClipsRepo, repos.ClipsRepo)

	// PR-D composition-root pre-rejection: every required dep MUST be
	// non-nil by the time we reach NewService. A nil here fails the
	// bundle build before any service is constructed, so the operator
	// sees the missing dep at startup rather than racing the late-bind
	// sequence (which used to live between BuildOutboxBundle returning
	// and catalogSync.SetDispatcher being called).
	if drive.DriveUploader == nil {
		return nil, fmt.Errorf("BuildSyncBundle: drive.DriveUploader is required (canonical catalogsync Drive uploader)")
	}
	if search.AssetIndexService == nil {
		return nil, fmt.Errorf("BuildSyncBundle: search.AssetIndexService is required (asset_index write side)")
	}
	if process.ClipIndexerService == nil {
		return nil, fmt.Errorf("BuildSyncBundle: process.ClipIndexerService is required (Qdrant indexer wiring)")
	}
	if outbox == nil || outbox.Dispatcher == nil {
		return nil, fmt.Errorf("BuildSyncBundle: outbox.Dispatcher is required — QDRANT-002 PR7 removed the legacy fallback; root.Outbox must be built first")
	}

	catalogSync, err := catalogsync.NewService(catalogsync.Deps{
		Uploader:    drive.DriveUploader,
		Targets:     syncTargets,
		AssetIndex:  search.AssetIndexService,
		AssetTree:   search.AssetTreeService,
		ClipIndexer: process.ClipIndexerService,
		Dispatcher:  outbox.Dispatcher,
		Log:         log,
	})
	if err != nil {
		return nil, fmt.Errorf("BuildSyncBundle: catalogsync.NewService: %w", err)
	}

	return &SyncBundle{
		CatalogSync: catalogSync,
	}, nil
}
