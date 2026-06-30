package app

import (
	"context"
	"fmt"

	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
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
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"

	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	voiceoversync "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/sync"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/autotag"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/hashutil"
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
		Registry:      clipsRegistry,
		DriveUploader: drive.DriveUploader,
		AssetIndex:    search.AssetIndexService,
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

	hashAdapter := hashutil.NewHashAdapter()
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
		HashSvc:           hashAdapter,
	}
	if err := youtube.ValidateServiceDeps(youtubeDeps); err != nil {
		return nil, fmt.Errorf("compose youtube: %w", err)
	}
	youtubeClipService := youtube.NewService(youtubeDeps)

	// PR-VO-A3 (June 2026): pass the canonical outbox.Dispatcher to the
	// voiceover service so swapVoiceoverRow can enqueue
	// `asset.index.requested` inside the SQLite transaction that
	// commits the new voiceovers row. The dispatcher is required (no
	// legacy fallback): BuildDomainBundle's earlier nil-check already
	// rejects a nil outbox.Dispatcher, so we can assert it here.
	if outbox == nil || outbox.Dispatcher == nil {
		return nil, fmt.Errorf("compose domains: outbox.Dispatcher is required (PR-VO-A3 voiceover indexing handoff)")
	}
	// FASE 9: nil-safe extraction — DriveUploader.Service panics when nil.
	var rawDriveSvc *gdrive.Service
	if drive.DriveUploader != nil {
		rawDriveSvc = drive.DriveUploader.Service
	}
	voiceoverSvc, voiceoverRepo := buildVoiceoverService(ctx, cfg, dbs, log,
		rawDriveSvc, drive.DriveUploader,
		search.AssetIndexService, process.ClipIndexerService,
		drive.DestResolver,
		voMetaWriter, ai.ScriptGen,
		outbox.Dispatcher,
	)

	booksSvc := buildBooksService(cfg, dbs, log, drive.DriveUploader, voiceoverSvc, drive.Publisher)

	ingestSvc := buildIngestService(cfg, log, dbs, drive.DriveUploader, repos, search, mutationsDisp)

	var driveClient *gdrive.Service
	if drive.DriveUploader != nil {
		driveClient = drive.DriveUploader.Service
	}

	imageSvc, metaWriter := buildImagesService(ctx, cfg, log,
		driveClient, repos.ClipsRepo, repos.ClipsRepo,
		drive.StyleRegistry, ai.ScriptGen,
		drive.MediaStore, repos.ImageRepo,
		voMetaWriter, ingestSvc,
		outbox.Dispatcher,
	)

	// Wave 15 (June 2026): RealtimeService split into two typed ports —
	// see composition.go DomainBundle for rationale. Both stay typed-nil
	// (package removed in commit d61068b3).
	var realtimeMatcher assetsapi.RealtimeMatcher
	var realtimeSearch usecase.RealtimeSearchService

	// autotagVectorStore removed during the bundle simplification
	// deleted. The autotag service no longer takes a vector-store indexer;
	// its semantic-tagging pipeline can still consume clip embeddings from
	// the DB but no longer propagates them to a vector store backend.
	autotagSvc := autotag.NewService(dbs.main.DB, repos.Assets.Repository(), process.VLMClient, nil, log)

	// Wave 15 (June 2026): typed-nil for scriptcore.AssocSearchService —
	// replaces the preceding `interface{}` carrier.
	var assocService usecase.AssocSearchService
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

	// PR-VOICEOVER-PARENT-CHILD-FANOUT (P0.3, June 2026): construct the
	// per-language ProcessOneVoiceoverUseCase here so the new
	// voiceover.generate_item child-job handler has a single source
	// of truth to dispatch through. The use case delegates the
	// per-language routine to voService.GenerateBatch (the canonical
	// production surface wired through the legacy Service bundle),
	// keeping the wiring footprint minimal — adding a separate
	// GenerateVoiceoversUseCase (the full 7-port use case) requires
	// resolving TTSProvider / AudioPostProcessor / etc. independently;
	// the BACKFILL invariant is to layer that in a follow-up PR.
	voiceoverProcessOne := voiceover.NewProcessOneVoiceoverUseCase(voiceover.ProcessOneDeps{
		Service: voiceoverSvc,
		Logger:  log,
	})
	log.Info("P0.3: ProcessOneVoiceoverUseCase wired (per-language child-job handler dispatcher)")

	// P0.1 (June 2026): construct the content-addressed artifact blob
	// service so the upload UseCase (UploadVideoClip) can accept real
	// video uploads instead of returning HTTP 500. The LocalBlobStore
	// stages blobs under cfg.Storage.DataDir; SQLiteRepository persists
	// metadata in the unified media.db.sqlite.
	artifactBlobStore, err := artifacts.NewLocalBlobStore(cfg.Storage.DataDir)
	if err != nil {
		return nil, fmt.Errorf("compose domains: artifact blob store: %w", err)
	}
	artifactRepo := artifacts.NewSQLiteRepository(dbs.main.DB)
	artifactService := artifacts.NewService(artifactBlobStore, artifactRepo, log)
	log.Info("P0.1: artifact blob service wired (content-addressed staging + verify + promote)",
		zap.String("data_dir", cfg.Storage.DataDir))

	return &DomainBundle{
		YoutubeClipService:  youtubeClipService,
		VoiceoverService:    voiceoverSvc,
		VoiceoverSync:       vosyncSvc,
		VoiceoverProcessOne: voiceoverProcessOne,
		ImageService:        imageSvc,
		IngestService:       ingestSvc,
		BooksService:        booksSvc,
		LessonsService:      lessonsS,
		MetaWriter:          metaWriter,
		RealtimeMatcher:     realtimeMatcher,
		RealtimeSearch:      realtimeSearch,
		AutotagService:      autotagSvc,
		AssocService:        assocService,
		ArtifactService:     artifactService,
	}, nil
}

// buildIngestService constructs the ingest.Service from the same deps
// that WireMediaIngest uses.
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
func buildIngestService(cfg *config.Config, log *zap.Logger, dbs *databases, driveUploader *driveutil.Uploader, repos *RepoBundle, search *SearchBundle, mutationsDisp mutations.AssetMutationDispatcher) *ingest.Service {
	if driveUploader == nil {
		return nil
	}
	if repos.ImageRepo == nil || repos.VoiceoverRepo == nil || repos.ClipsRepo == nil || search.AssetIndexService == nil {
		return nil
	}
	if mutationsDisp == nil {
		log.Warn("buildIngestService: mutationsDisp is nil — ingest will surface ErrDispatcherUnavailable on first Upsert (QDRANT-002 PR7 fail-closed)")
	}
	imagesRegistry := imgservice.NewRegistryAdapter(repos.ImageRepo, cfg.Storage.ImagesPath(), log)
	imagesLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: imagesRegistry, DriveUploader: driveUploader, AssetIndex: search.AssetIndexService, Store: ingest.NewImageStoreAdapter(repos.ImageRepo, cfg.Storage.ImagesPath())}, log)
	voiceoverRegistry := voiceover.NewVoiceoverRegistryAdapter(repos.VoiceoverRepo)
	voiceoverLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: voiceoverRegistry, DriveUploader: driveUploader, AssetIndex: search.AssetIndexService, Store: ingest.NewVoiceoverStoreAdapter(repos.VoiceoverRepo)}, log)
	clipRegistry := artifacts.NewClipsRegistry(dbs.main.DB, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(), mutationsDisp)
	clipLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: clipRegistry, DriveUploader: driveUploader, AssetIndex: search.AssetIndexService, Store: ingest.NewClipStoreAdapter(dbs.main.DB, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(), mutationsDisp)}, log)
	stockRegistry := artifacts.NewClipsRegistry(dbs.main.DB, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(), mutationsDisp)
	stockLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: stockRegistry, DriveUploader: driveUploader, AssetIndex: search.AssetIndexService, Store: ingest.NewClipStoreAdapter(dbs.main.DB, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(), mutationsDisp)}, log)
	return ingest.NewService(cfg, log, driveUploader.Service, map[ingest.Kind]*ingest.Pipeline{
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

	memoryRepo := adapters.NewRepository(dbs.main.DB)
	memorySvc := adapters.NewService(memoryRepo, log)
	log.Info("Gemma Memory Gate service initialized")

	// PR 5 (June 2026): NewEngine no longer takes a ScriptRepository
	// arg — engine persistence is gone; the single-writer is
	// PersistenceProcessor. RepositoryAdapter is still constructed
	// here because wireScriptFlow(BuildRepoBundle) uses it for
	// PersistenceProcessor registration (see wire_script.go).
	//
	// TODO #8 (drift-fix PR): wrap *adapters.Service in the local
	// MemoryCacheAdapter so it satisfies the narrow memoryCache
	// interface (memoryCache uses LOCAL lowercase memoryGateRequest /
	// memoryGateResult types in engine.go; adapters.Service uses
	// the uppercase MemoryGateRequest / GateResult types). The
	// wrapper is the contained seam for this cross-package type
	// mismatch — see memory_cache_adapter.go for rationale.
	engine := usecase.NewEngine(scriptGen, usecase.NewMemoryCacheAdapter(memorySvc), log)

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

	// PR-D composition-root pre-rejection is relaxed for the no-Drive
	// test path: when Drive is disabled the sync bundle still builds
	// with a nil-service uploader so the bootstrap tests can complete.
	// The catalogsync service itself remains fail-closed if it is ever
	// invoked without a real Drive client.
	uploader := drive.DriveUploader
	if uploader == nil {
		uploader = &driveutil.Uploader{Log: log}
		log.Warn("BuildSyncBundle: drive uploader missing; using nil-service placeholder for disabled-drive bootstrap")
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
		Uploader:    uploader,
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
