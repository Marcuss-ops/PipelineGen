package app

import (
	"strings"

	common "github.com/Marcuss-ops/PipelineGen/internal/api/common"
	"github.com/Marcuss-ops/PipelineGen/internal/application/association"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/realtime"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/core/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/core/processor"
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/catalog"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/media/autotag"
	"github.com/Marcuss-ops/PipelineGen/internal/media/books"
	"github.com/Marcuss-ops/PipelineGen/internal/media/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/media/generation"
	lessonsService "github.com/Marcuss-ops/PipelineGen/internal/media/lessons"
	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	"github.com/Marcuss-ops/PipelineGen/internal/media/voiceoversync"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"

	"context"
	"fmt"
	"net/http"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/core/destination"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/vlm"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/scheduler"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/media/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/application/youtube"
	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"os"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/api/script"
	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	scriptcore "github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/reranker"
	pkgffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"github.com/Marcuss-ops/PipelineGen/internal/media"
	"github.com/Marcuss-ops/PipelineGen/internal/media/lessons"
	"github.com/Marcuss-ops/PipelineGen/internal/media/videomuscles"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/embeddings"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

type services struct {
	scriptGen          *ollama.Generator
	docClient          drive.DocClient
	driveUploader      *drive.Uploader
	driveClient        *gdrive.Service
	utility            *common.UtilityHandler
	scriptsRepo        *sqlitescripts.ScriptRepository
	imageRepo          *assets.ImagesRepository
	imageService       *imgservice.Service
	clipsRepo          *assets.ClipsRepository // unified (replaces stockDriveRepo, artlistRepo, clipsOnlyRepo)
	assetRepo          asset.Repository
	driveDests         *DriveDestinations // resolved Drive folder IDs (immutable Config)
	monitorsRepo       *assets.MonitorsRepository
	voiceoverService   *voiceover.Service
	voiceoverSync      *voiceoversync.Service
	clipIndexerService *clipindexer.Service
	catalogRepo        *catalog.Repository
	catalogSync        *catalogsync.Service
	assocService       *association.Service
	jobsRepo           *appjobs.SQLiteStore
	jobsService        *appjobs.Service
	jobServiceFacade   *job.Service
	jobsDispatcher     *appjobs.Dispatcher
	memoryRepo         *gemmamemory.Repository
	mediaProcessor     processor.Processor
	ollamaClient       *client.Client
	youtubeClipService *youtube.Service
	assetIndexService  *assetindex.Service
	assetTreeService   *assettree.Service
	assetResolver      *assetindex.Resolver
	lifecycleScheduler *scheduler.LifecycleScheduler
	maintenanceSvc     *maintenance.Service
	styleRegistry      *generation.StyleRegistry
	vectorSvc          *vectorstore.Service
	realtimeSvc        *realtime.Service
	vlmClient          *vlm.Client
	autotagService     *autotag.Service
	booksService       *books.Service
	lessonsService     *lessonsService.Service

	mediaStore *drive.Store

	// outboxDispatcher is the canonical ingestion entry point. Injected
	// into ingestion flows (catalogsync, voiceover, artlist orchestrator,
	// stock upload, youtube registration, manual upload, …). Admin reindex
	// uses outbox.DirectIndexer instead — the dispatcher is for production
	// writes only.
	outboxDispatcher *outbox.Dispatcher

	// Outbox events (PR5) — reliable outbox for asset.index.requested,
	// delivery, metadata_export, provider_sync, workflow.step.* handlers.
	// Replaces the legacy media_index_outbox Worker pool.
	outboxEventsRepo     *outboxevents.Repository
	outboxEventsPool     *outboxevents.Pool
	outboxEventsRegistry *outboxevents.HandlerRegistry

	// Asset satellite tables (canonical model completion, PR0)
	assetLocationsRepo  asset.LocationRepository
	assetProcessingRepo asset.ProcessingRepository
	assetVersionsRepo   asset.VersionRepository

	assetsSvc *asset.Service
}

// initServices initializes the full service graph by delegating to three
// domain-specific composers in dependency order:
//
//  1. composeCoreInfra    — LLM, Drive, storage, media processor, vector infra
//  2. composeMediaDomain  — YouTube, voiceover, images, books
//  3. composeIntegration  — sync, jobs, realtime, script flow, deletion, lessons
//
// Each composer returns a focused struct; initServices stitches them together
// into the single *services struct expected by the rest of the app.
func initServices(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, registryWiring *RegistryWiring) (*services, error) {
	// 1. Core Infrastructure (shared dependencies)
	core, err := composeCoreInfra(ctx, cfg, dbs, log)
	if err != nil {
		return nil, err
	}

	// 2. Media Domain Services (depend on core infra)
	mediaDomain, err := composeMediaDomain(ctx, cfg, dbs, log, core)
	if err != nil {
		return nil, err
	}

	// 3. Cross-domain Integration (builds the final services struct, late-
	// binds outbox dispatcher onto stockpipeline.Service via registryWiring
	// when the registry was assembled upstream). nil registryWiring is
	// tolerated — partial deployments, test harnesses, and the legacy
	// entry points stay green by skipping the late-binding step.
	return composeIntegration(ctx, cfg, dbs, log, core, mediaDomain, registryWiring)
}

// initVoiceoverService sets up the voiceover service and its repository.
func initVoiceoverService(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger,
	driveClient *gdrive.Service, driveUploader *drive.Uploader,
	assetIndexService *assetindex.Service, clipIndexerService *clipindexer.Service,
	destResolver destination.Resolver) (*voiceover.Service, *assets.VoiceoversRepository) {

	voDir := cfg.Storage.VoiceoversPath()
	voRepo := assets.NewVoiceoversRepository(dbs.main.DB)

	// Create voiceover registry adapter
	voRegistryAdapter := voiceover.NewVoiceoverRegistryAdapter(voRepo)

	// Create LifecycleService for voiceover using common factory
	voLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    voRegistryAdapter,
		DriveClient: driveClient,
		AssetIndex:  assetIndexService,
	}, log)

	voService := voiceover.NewService(cfg, dbs.main.DB, cfg.Paths.PythonScriptsDir, voDir, log, driveUploader, voLifecycle, destResolver)
	log.Info("Voiceover service initialized", zap.String("python_scripts_dir", cfg.Paths.PythonScriptsDir))

	// Wire clip indexer for voiceover embedding + Qdrant upsert
	if clipIndexerService.IsEnabled() {
		voService.SetClipIndexer(func(ctx context.Context, assetID string) error {
			return clipIndexerService.IndexClip(ctx, assetID)
		})
		log.Info("clip indexer wired into voiceover service for semantic search")
	}

	// Wire Ollama translator for promo voiceover generation
	return voService, voRepo
}

// initBooksService creates the books processing service.
func initBooksService(cfg *config.Config, dbs *databases, log *zap.Logger, driveUploader *drive.Uploader, voiceoverSvc *voiceover.Service) *books.Service {
	booksSvc := books.NewService(&books.Config{
		Enabled:       cfg.Books.Enabled,
		ScriptPath:    cfg.Books.ScriptPath,
		PythonBin:     cfg.Books.PythonBin,
		DriveFolderID: cfg.Drive.BooksFolder(),
	}, dbs.main.DB, cfg.Drive.BooksFolder(), log, voiceoverSvc)
	if driveUploader != nil {
		booksSvc.SetDriveUploader(driveUploader)
	}
	log.Info("Books service initialized", zap.Bool("enabled", cfg.Books.Enabled))
	return booksSvc
}

// initImageService creates the image generation service and metadata writer.
func initImageService(ctx context.Context, cfg *config.Config, log *zap.Logger,
	driveClient *gdrive.Service, clipsRepo *assets.ClipsRepository, artlistRepo *assets.ClipsRepository,
	styleRegistry *generation.StyleRegistry, scriptGen *ollama.Generator,
	mediaStore *drive.Store, vectorSvc *vectorstore.Service,
	imageRepo *assets.ImagesRepository) (*imgservice.Service, *semantic.MetadataWriter) {

	imageService := imgservice.NewService(cfg, imageRepo, clipsRepo, driveClient, styleRegistry, log)
	imageService.SetNvidiaConfig(cfg.External.NvidiaAPIKey, cfg.External.NvidiaModel)
	imageService.SetGoogleAccountingConfig(
		cfg.GoogleAccounting.ServerURL,
		cfg.GoogleAccounting.DownloadDir,
		cfg.GoogleAccounting.VidsProjectID,
	)

	// Wire remote image endpoint (Google Flow on external server)
	if cfg.External.RemoteImageEndpointURL != "" {
		imageService.SetRemoteImageEndpointURL(cfg.External.RemoteImageEndpointURL)
		log.Info("Remote image endpoint configured", zap.String("url", cfg.External.RemoteImageEndpointURL))
	}

	// Wire Velox base URL for push-mode webhook delivery
	if cfg.External.VeloxBaseURL != "" {
		imageService.SetVeloxBaseURL(cfg.External.VeloxBaseURL)
		log.Info("Velox base URL for webhook push configured", zap.String("url", cfg.External.VeloxBaseURL))
	}

	imageService.SetMediaStore(mediaStore)
	imageService.SetLLMGenerator(scriptGen)
	if vectorSvc != nil {
		imageService.SetVectorStore(vectorSvc)
	}

	// Wire unified metadata writer into image service
	metaWriter := semantic.NewMetadataWriter(
		cfg.Paths.PythonScriptsDir,
		cfg.Storage.TempPath(),
		cfg.External.OllamaURL,
		cfg.External.OllamaModel,
		log,
	)
	imageService.SetMetadataWriter(metaWriter)

	return imageService, metaWriter
}

// CoreInfra holds core infrastructure services produced by composeCoreInfra.
type CoreInfra struct {
	OllamaClient  *client.Client
	ScriptGen     *ollama.Generator
	DocClient     drive.DocClient
	DriveClient   *gdrive.Service
	DriveUploader *drive.Uploader
	StyleRegistry *generation.StyleRegistry
	DriveDests    *DriveDestinations // resolved Drive folder IDs (immutable Config)

	ClipsOnlyRepo       *assets.ClipsRepository
	AssetRepo           asset.Repository
	AssetLocationRepo   asset.LocationRepository
	AssetProcessingRepo asset.ProcessingRepository
	AssetsSvc           *asset.Service
	MediaProcessor      processor.Processor
	AssetIndexService   *assetindex.Service
	AssetTreeService    *assettree.Service
	ClipIndexerService  *clipindexer.Service
	VLMClient           *vlm.Client
	VectorSvc           *vectorstore.Service
	MediaStore          *drive.Store
	DestResolver        destination.Resolver
}

// composeCoreInfra initializes all core infrastructure services.
// These are shared dependencies consumed by higher-level domain services.
func composeCoreInfra(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger) (*CoreInfra, error) {
	styleRegistry, _ := generation.NewStyleRegistry("config/generation_styles.yaml")

	// 1. LLM & Script Generation
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

	// 2. Drive Clients
	docClient, err := drive.NewDocClient(ctx, cfg.GetCredentialsPath(), cfg.GetTokenPath())
	if err != nil {
		log.Warn("Docs client not initialized", zap.Error(err))
	}

	driveClient, err := drive.NewDriveServiceFromFiles(ctx, cfg)
	if err != nil {
		log.Warn("Google Drive client not initialized", zap.Error(err))
	}
	var driveUploader *drive.Uploader
	var dests *DriveDestinations
	if driveClient != nil {
		driveUploader = &drive.Uploader{Service: driveClient, Log: log}
		dests = resolveRuntimeDestinations(ctx, dbs.main.DB, driveClient, cfg, log)
		imageRoot := dests.VideoAIFolder()
		if imageRoot != "" && imageRoot != dests.MediaRoot {
			go ensureStyleDriveFolders(ctx, driveUploader, imageRoot, styleRegistry, log)
			log.Info("Style Drive folders using AI Images root", zap.String("folder_id", imageRoot))
		}
		// Validate critical Drive folders are accessible at startup
		driveFolderIDs := map[string]string{
			"images":   dests.ImagesFolder(),
			"video_ai": dests.VideoAIFolder(),
		}
		for name, folderID := range driveFolderIDs {
			if folderID == "" {
				continue
			}
			_, err := driveClient.Files.Get(folderID).Fields("id, name").Context(ctx).Do()
			if err != nil {
				log.Warn("Drive folder validation failed at startup",
					zap.String("folder_name", name),
					zap.String("folder_id", folderID),
					zap.Error(err),
				)
			} else {
				log.Info("Drive folder validated",
					zap.String("folder_name", name),
					zap.String("folder_id", folderID),
				)
			}
		}
	} else {
		// No Drive client — populate from config values only
		dests = configOnlyDestinations(cfg)
	}

	// 3. Storage Directories
	storageDirs := []string{
		cfg.Storage.DataDir,
		cfg.Storage.VoiceoversPath(),
		cfg.Storage.AssetsPath(),
		cfg.Storage.DownloadsPath(),
		cfg.Storage.BackupsPath(),
		cfg.Storage.TempPath(),
		cfg.Storage.AnimationsPath(),
		cfg.Storage.YoutubeClipsPath(),
		cfg.Storage.ArtlistPath(),
		cfg.Storage.ImagesPath(),
	}
	for _, dir := range storageDirs {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Warn("Failed to create storage directory", zap.String("path", dir), zap.Error(err))
		}
	}

	// 4. Media Processing
	assetsStore := asset.NewAssetStoreSQLite(dbs.main.DB, log)
	assetsSvc := asset.NewService(assetsStore, log)

	assetRepo := assetsSvc.Repository()
	clipsOnlyRepo := assets.NewClipsRepositoryCanonical(dbs.main.DB, log, assetRepo)
	assetLocRepo := assetsSvc.LocationRepository()
	assetProcRepo := assetsSvc.ProcessingRepository()
	mediaProcessor := initMediaProcessor(cfg, dbs.main.DB, assetRepo, assetsSvc, assetLocRepo, assetProcRepo, log, driveUploader)

	// 5. Asset Services
	assetIndexService, assetTreeService, err := initAssetServices(dbs, log)
	if err != nil {
		return nil, err
	}

	// 6. Clip Indexer & VLM
	vlmClient := vlm.NewClient(vlm.Config{
		Enabled:   cfg.VLM.Enabled,
		Endpoint:  cfg.VLM.URL,
		Model:     cfg.VLM.Model,
		TimeoutMs: cfg.VLM.TimeoutMs,
		Weight:    cfg.VLM.Weight,
	})

	clipIndexerService := clipindexer.NewService(&clipindexer.Config{
		Enabled:               cfg.ClipIndexer.Enabled,
		ServerURL:             cfg.ClipIndexer.ServerURL,
		ScriptPath:            cfg.ClipIndexer.ScriptPath,
		PythonBin:             cfg.ClipIndexer.PythonBin,
		AutoIndexAfterArtlist: cfg.ClipIndexer.AutoIndexAfterArtlist,
		DBPath:                dbs.main.Path(),
	}, dbs.main.DB, dbs.main.Path(), log)

	// 7. Vector Store & Media Store
	var vectorSvc *vectorstore.Service
	if cfg.VectorSearch.Enabled {
		qdrantCfg := vectorstore.Config{
			URL:                  cfg.VectorSearch.URL,
			Collection:           cfg.VectorSearch.Collection,
			TextVectorName:       cfg.VectorSearch.TextVectorName,
			VisualVectorName:     cfg.VectorSearch.VisualVectorName,
			AudioVectorName:      cfg.VectorSearch.AudioVectorName,
			TranscriptVectorName: cfg.VectorSearch.TranscriptVectorName,
			SparseVectorName:     cfg.VectorSearch.SparseVectorName,
			TextDimensions:       cfg.VectorSearch.TextDimensions,
			VisualDimensions:     cfg.VectorSearch.VisualDimensions,
			AudioDimensions:      cfg.VectorSearch.AudioDimensions,
			TranscriptDimensions: cfg.VectorSearch.TranscriptDimensions,
			MinInstantScore:      cfg.VectorSearch.MinInstantScore,
			TimeoutMs:            cfg.VectorSearch.TimeoutMs,
			CollectionVersion:    cfg.VectorSearch.CollectionVersion,
			CollectionAlias:      cfg.VectorSearch.CollectionAlias,
			DisableAlias:         cfg.VectorSearch.DisableAlias,
		}
		if cfg.VectorSearch.CollectionVersion != "" {
			mode := "alias-routed"
			if cfg.VectorSearch.DisableAlias {
				mode = "versioned-direct"
			}
			log.Info("Qdrant collection versioning enabled",
				zap.String("collection", cfg.VectorSearch.Collection),
				zap.String("version", cfg.VectorSearch.CollectionVersion),
				zap.String("alias", cfg.VectorSearch.CollectionAlias),
				zap.String("routing", mode))
		}
		qdrantClient := vectorstore.NewQdrantClient(qdrantCfg)
		vectorSvc = vectorstore.NewService(qdrantClient, qdrantCfg, log)
		// Apply operator-tunable retry policy. Defaults are conservative
		// (3 attempts, 200ms→5s backoff); see cfg.VectorSearch.* for
		// env-overridable knobs. SetRetryPolicy is a no-op when any
		// arg is <=0, so the wiring is safe even with empty config.
		vectorSvc.SetRetryPolicy(
			cfg.VectorSearch.RetryAttempts,
			time.Duration(cfg.VectorSearch.RetryInitialWaitMs)*time.Millisecond,
			time.Duration(cfg.VectorSearch.RetryMaxWaitMs)*time.Millisecond,
		)
		log.Info("Qdrant retry policy applied",
			zap.Int("attempts", cfg.VectorSearch.RetryAttempts),
			zap.Int("initial_wait_ms", cfg.VectorSearch.RetryInitialWaitMs),
			zap.Int("max_wait_ms", cfg.VectorSearch.RetryMaxWaitMs),
		)
		if err := vectorSvc.EnsureCollection(ctx); err != nil {
			log.Warn("vector store collection setup failed (will retry on upsert)", zap.Error(err))
		}
		clipIndexerAdapter := vectorstore.NewClipIndexerAdapter(dbs.main.DB, vectorSvc, qdrantCfg, log)
		if clipIndexerAdapter != nil {
			clipIndexerService.SetVectorStore(clipIndexerAdapter)
			log.Info("vector store enabled for clip indexer")
		}
	}

	storageResolver := drive.NewResolver(
		drive.MediaRoot(cfg.Storage.MediaPath()),
		drive.DriveRoot(dests.RootFolder()),
	)
	mediaStore := drive.NewStore(storageResolver, driveUploader, dests.RootFolder(), dests.ImagesFolder(), dests.VideoAIRoot, dests.SoundEffectsRoot, log)
	mediaStore.SetAssetTree(assetTreeService)
	if dests.VideoAIRoot != "" {
		mediaStore.SetTreeSource(dests.VideoAIRoot, "videoai")
	}
	if dests.ImagesFolder() != "" {
		mediaStore.SetTreeSource(dests.ImagesFolder(), "image")
	}
	log.Info("mediaStore: Drive roots configured",
		zap.String("images_folder_id", dests.ImagesFolder()),
		zap.String("video_ai_folder_id", dests.VideoAIFolder()),
	)
	destResolver := drive.NewDestinationResolver(mediaStore)

	return &CoreInfra{
		OllamaClient:  ollamaClient,
		ScriptGen:     scriptGen,
		DocClient:     docClient,
		DriveClient:   driveClient,
		DriveUploader: driveUploader,
		StyleRegistry: styleRegistry,
		DriveDests:    dests,

		ClipsOnlyRepo:       clipsOnlyRepo,
		AssetRepo:           assetRepo,
		AssetLocationRepo:   assetLocRepo,
		AssetProcessingRepo: assetProcRepo,
		AssetsSvc:           assetsSvc,
		MediaProcessor:      mediaProcessor,
		AssetIndexService:   assetIndexService,
		AssetTreeService:    assetTreeService,
		ClipIndexerService:  clipIndexerService,
		VLMClient:           vlmClient,
		VectorSvc:           vectorSvc,
		MediaStore:          mediaStore,
		DestResolver:        destResolver,
	}, nil
}

// composeRealtimeService creates the real-time matching service when enabled.
//
// PR3-5b.4: clips (media_assets canonical metadata) and the
// outboxevents.Repository (outbox_events counters) are threaded explicitly so
// realtime.IndexHealth can run the canonical sqlite<->qdrant cross-check.
// Both are optional — the service logs a WARN at startup if missing and the
// cross-check falls back to zeros — but production wiring MUST pass non-nil.
func composeRealtimeService(ctx context.Context, cfg *config.Config, log *zap.Logger, vectorSvc *vectorstore.Service, clipsRepo *assets.ClipsRepository, outboxEventsRepo *outboxevents.Repository, jobsService *job.Service) *realtime.Service {
	embedder := realtime.NewPythonEmbeddingAdapter(cfg.ClipIndexer.ServerURL)
	jobAdapter := realtime.NewJobServiceAdapter(jobsService, log)
	rerankerClient := reranker.NewClient(reranker.Config{
		Enabled:   cfg.Reranker.Enabled,
		URL:       cfg.Reranker.URL,
		Model:     cfg.Reranker.Model,
		TopK:      cfg.Reranker.TopK,
		TimeoutMs: cfg.Reranker.TimeoutMs,
		Weight:    cfg.Reranker.Weight,
	})
	realtimeSvc := realtime.NewService(vectorSvc, embedder, jobAdapter, rerankerClient, cfg.Reranker, &cfg.VectorSearch, clipsRepo, outboxEventsRepo, log)
	log.Info("real-time matching service enabled",
		zap.Bool("reranker_enabled", cfg.Reranker.Enabled),
		zap.Int("reranker_top_k", cfg.Reranker.TopK),
		zap.Int("reranker_timeout_ms", cfg.Reranker.TimeoutMs),
		zap.Bool("index_health_clips_wired", clipsRepo != nil),
		zap.Bool("index_health_outbox_wired", outboxEventsRepo != nil),
	)
	return realtimeSvc
}

// MediaDomain holds media-specific services produced by composeMediaDomain.
type MediaDomain struct {
	YoutubeClipService *youtube.Service
	VoiceoverService   *voiceover.Service
	VoiceoverRepo      *assets.VoiceoversRepository
	BooksService       *books.Service
	ClipsRepo          *assets.ClipsRepository // single shared repository (replaces ClipsRepo + ArtlistRepo)
	ScriptsRepo        *sqlitescripts.ScriptRepository
	ImageRepo          *assets.ImagesRepository
	ImageService       *imgservice.Service
	MetaWriter         *semantic.MetadataWriter
	MonitorsRepo       *assets.MonitorsRepository
}

// composeMediaDomain initializes all media domain services.
// These depend on core infrastructure and domain-specific configuration.
func composeMediaDomain(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, core *CoreInfra) (*MediaDomain, error) {
	// Single shared clips repository — core.ClipsOnlyRepo is the canonical instance.
	// compose_media previously created separate clipsRepo and artlistRepo instances
	// on the same DB; PR 7 unified them into one shared pointer.
	clipsRepo := core.ClipsOnlyRepo
	scriptsRepo := sqlitescripts.NewScriptRepository(dbs.main.DB)
	imageRepo := assets.NewImagesRepository(dbs.main.DB)

	// YouTube Lifecycle & Video Pipeline
	clipsRegistry := artifacts.NewClipsRegistry(
		dbs.main.DB,
		core.AssetRepo,
		core.AssetsSvc,
		core.AssetLocationRepo,
		core.AssetProcessingRepo,
	)
	ytLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    clipsRegistry,
		DriveClient: core.DriveClient,
		AssetIndex:  core.AssetIndexService,
	}, log)

	clipProcessor := pkgffmpeg.NewFromConfig(cfg)
	videoPipeline := videomuscles.NewPipeline(cfg, log, clipProcessor)

	// YouTube Clip Service
	monitorsRepo := assets.NewMonitorsRepository(dbs.main.DB)
	youtubeClipService := youtube.NewService(
		cfg, log,
		core.ClipsOnlyRepo, monitorsRepo,
		core.DriveClient, core.MediaProcessor,
		videoPipeline, ytLifecycle,
		core.ClipIndexerService, core.DestResolver,
		core.OllamaClient,
		nil, nil, // assetProcessing, assetVersions — wired below via late-binding
	)

	// Voiceover Service
	voService, voRepo := initVoiceoverService(ctx, cfg, dbs, log,
		core.DriveClient, core.DriveUploader,
		core.AssetIndexService, core.ClipIndexerService,
		core.DestResolver,
	)

	// Books Service
	booksSvc := initBooksService(cfg, dbs, log, core.DriveUploader, voService)

	// Image Service
	imageService, metaWriter := initImageService(ctx, cfg, log,
		core.DriveClient, clipsRepo, clipsRepo,
		core.StyleRegistry, core.ScriptGen,
		core.MediaStore, core.VectorSvc, imageRepo,
	)

	// Wire semantic tagger for voiceover metadata enrichment
	voService.SetSemanticTagger(func(ctx context.Context, prompt, style, mediaType, generator string) (*voiceover.SemanticTaggerResult, error) {
		payload, _, err := metaWriter.GeneratePayload(ctx, semantic.WriteRequest{
			AssetID:   "",
			AssetType: "voiceover",
			MediaType: mediaType,
			Source:    "voiceover",
			Generator: generator,
			Style:     style,
			Prompt:    prompt,
		})
		if err != nil {
			return nil, err
		}
		return &voiceover.SemanticTaggerResult{
			SearchText: payload.SearchText,
			Tags:       payload.Tags,
			Subjects:   payload.Subjects,
			Mood:       payload.Mood,
		}, nil
	})

	// Wire Ollama translator for voiceover promo generation
	if core.ScriptGen != nil {
		voService.SetTranslator(func(ctx context.Context, text, targetLanguage string) (string, error) {
			return core.ScriptGen.TranslateText(ctx, text, targetLanguage)
		})
		log.Info("Ollama translator wired into voiceover service for promo generation")
	}

	return &MediaDomain{
		YoutubeClipService: youtubeClipService,
		VoiceoverService:   voService,
		VoiceoverRepo:      voRepo,
		BooksService:       booksSvc,
		ClipsRepo:          clipsRepo,
		ScriptsRepo:        scriptsRepo,
		ImageRepo:          imageRepo,
		ImageService:       imageService,
		MetaWriter:         metaWriter,
		MonitorsRepo:       monitorsRepo,
	}, nil
}

// composeIntegration initializes cross-domain integration services and builds the final services struct.
func composeIntegration(
	ctx context.Context,
	cfg *config.Config,
	dbs *databases,
	log *zap.Logger,
	core *CoreInfra,
	mediaDomain *MediaDomain,
	registryWiring *RegistryWiring,
) (*services, error) {
	// ── Asset Resolver, Association, Catalog Sync ──────────────────────
	clipsRepos := map[string]*assets.ClipsRepository{
		"youtube": core.ClipsOnlyRepo,
		"stock":   core.ClipsOnlyRepo,
		"artlist": core.ClipsOnlyRepo,
	}
	resolverCfg := &assetindex.ResolverConfig{
		ClipsRepos:    clipsRepos,
		ImageRepo:     mediaDomain.ImageRepo,
		VoiceoverRepo: mediaDomain.VoiceoverRepo,
	}
	assetResolver := assetindex.NewResolver(core.AssetIndexService, resolverCfg, log)
	log.Info("asset resolver initialized")

	catalogRepo := catalog.NewRepository(core.ClipsOnlyRepo, core.ClipsOnlyRepo, core.ClipsOnlyRepo)

	assocService := association.NewService(cfg.Storage.DataDir, "node-scraper", cfg.Paths.PythonScriptsDir,
		core.ClipsOnlyRepo, core.ClipsOnlyRepo, core.ClipsOnlyRepo, catalogRepo)
	// PR-D.5.1: inject the canonical Embedder (Python subprocess) so
	// application/association/embeddings.go no longer shells out directly.
	embedder := embeddings.NewPythonScriptEmbedder("python3", cfg.Paths.PythonScriptsDir)
	assocService.SetEmbedder(embedder)
	log.Info("embedding.Embedder injected into association service (infrastructure/embeddings/python)")
	if core.VectorSvc != nil {
		assocService.SetVectorStore(core.VectorSvc)
		log.Info("vector store wired into association service for hybrid search")
	}

	syncTargets := buildSyncTargets(cfg, core.ClipsOnlyRepo, core.ClipsOnlyRepo, core.ClipsOnlyRepo)
	catalogSync := catalogsync.NewService(core.DriveUploader, syncTargets, core.AssetIndexService, core.AssetTreeService, core.ClipIndexerService, log)

	var voiceoverSync *voiceoversync.Service
	if voFolder := cfg.Drive.VoiceoverFolder(); voFolder != "" && mediaDomain.VoiceoverRepo != nil {
		voiceoverSync = voiceoversync.NewService(core.DriveUploader, mediaDomain.VoiceoverRepo, core.AssetTreeService, voFolder, log)
		log.Info("Voiceover sync service initialized", zap.String("root_folder_id", voFolder))
	}

	// ── Jobs System ────────────────────────────────────────────────────
	// Construction is owned by module_jobs.go::BuildJobsBundle (Phase-B
	// ownership inversion). Local aliases below keep the late-binding sites
	// (CatalogSync.RegisterHandler, YoutubeClip/Voiceover/Books/Lessons
	// RegisterHandler, Realtime adapter, outboxdeps.Jobs, ...) unchanged.
	jobsBundle, err := BuildJobsBundle(dbs.main.DB, log)
	if err != nil {
		return nil, fmt.Errorf("failed to build jobs bundle: %w", err)
	}
	jobsRepo := jobsBundle.Repo
	jobsDispatcher := jobsBundle.Dispatcher
	jobsService := jobsBundle.Service
	jobServiceFacade := jobsBundle.Facade

	// Register Job Handlers
	catalogSync.RegisterHandler(jobsService)
	catalogSync.RegisterDriveFolderSyncHandler(jobsService)
	mediaDomain.YoutubeClipService.RegisterHandler(jobsService)
	mediaDomain.VoiceoverService.RegisterHandler(jobsService)
	mediaDomain.BooksService.RegisterJobHandler(jobsService)
	core.ClipIndexerService.RegisterJobHandler(jobsService)

	// ── Outbox Events Repository (PR5) ─────────────────────────────────
	// The canonical outbox_events table is the single source of truth for
	// asynchronous event dispatch (indexing, delivery, metadata, provider
	// sync, workflow steps). Constructed BEFORE composeRealtimeService so
	// IndexHealth can report pending/dead_letter counts from outbox_events.
	outboxEventsRepo := outboxevents.NewRepository(dbs.main.DB)

	// ── Outbox Events Dispatcher (canonical ingestion entry point) ─────
	// The outbox.Dispatcher now enqueues to outbox_events (not
	// media_index_outbox). MultiClipsUpserter routes clip.Source to the
	// appropriate repository.
	multiClipsUp := outbox.NewMultiClipsUpserter(
		map[string]outbox.ClipsUpserter{
			"youtube": core.ClipsOnlyRepo,
			"stock":   core.ClipsOnlyRepo,
			"artlist": core.ClipsOnlyRepo,
		},
		core.ClipsOnlyRepo, // default fallback for unknown clip.Source
		log,
	)
	outboxTxMgr := outbox.NewManager(dbs.main.DB, log)
	outboxDispatcher := outbox.NewDispatcher(multiClipsUp, outboxEventsRepo, outboxTxMgr, log)
	log.Info("outbox dispatcher instantiated: canonical upsert+outbox_events enqueue path")

	// Inject the canonical dispatcher into catalogsync — replaces the
	// `repo.UpsertClip; concurrent.SafeGoFunc(IndexClip)` pattern with an
	// atomic upsert+outbox_events enqueue transaction.
	catalogSync.SetDispatcher(outboxDispatcher)
	log.Info("outbox dispatcher wired into catalogsync")

	// Inject the canonical dispatcher into stockpipeline. The stock
	// service was constructed inside WireStockPipeline during
	// WireRegistry (before the dispatcher existed), so the setter is
	// invoked here in a late-binding step.
	if registryWiring != nil && registryWiring.StockPipeline != nil && registryWiring.StockPipeline.Service != nil {
		registryWiring.StockPipeline.Service.SetDispatcher(outboxDispatcher)
		log.Info("outbox dispatcher wired into stockpipeline (legacy SafeGoFunc(IndexClip) gated)")
	}

	// ── Real-time Matching Service ───────────────────────────────────
	var realtimeSvc *realtime.Service
	if cfg.VectorSearch.Enabled && cfg.VectorSearch.RealtimeEnabled && core.VectorSvc != nil {
		realtimeSvc = composeRealtimeService(ctx, cfg, log, core.VectorSvc, core.ClipsOnlyRepo, outboxEventsRepo, jobServiceFacade)
	}

	// ── Books API Handler ──────────────────────────────────────────────
	if mediaDomain.VoiceoverService != nil {
		mediaDomain.BooksService.SetVoiceoverService(mediaDomain.VoiceoverService)
	}

	// ── Gemma Memory & Script Engine ───────────────────────────────────
	memoryRepo := gemmamemory.NewRepository(dbs.main.DB)
	memorySvc := gemmamemory.NewService(memoryRepo, log)
	log.Info("Gemma Memory Gate service initialized")

	// Create adapter so the concrete sqlitescripts.ScriptRepository can be used
	// as a scripts.ScriptRepository interface by the engine, script flow handler,
	// and batch service.
	scriptsRepoAdapter := scriptcore.NewRepositoryAdapter(mediaDomain.ScriptsRepo)

	engine := scriptcore.NewEngine(core.ScriptGen, memorySvc, scriptsRepoAdapter, log)
	scriptFlowHandler := scriptpkg.NewScriptFlowHandler(
		core.ScriptGen, engine, mediaDomain.ImageService, realtimeSvc, assocService,
		mediaDomain.VoiceoverService, core.AssetTreeService, core.DocClient, core.DriveUploader,
		jobServiceFacade, scriptsRepoAdapter, memorySvc,
		cfg.Drive.ScriptsGenFolder(), cfg, log,
	)

	// ── Batch Service ───────────────────────────────────────────────────
	batchSvc := scripts.NewBatchService(cfg, log, core.ScriptGen, engine, core.DocClient, mediaDomain.VoiceoverService, scriptsRepoAdapter)
	scriptFlowHandler.SetBatchService(batchSvc)

	// ── Curation Service ───────────────────────────────────────────────
	curationSvc := scripts.NewCurationService(nil, jobsService, log)
	scriptFlowHandler.SetCurationService(curationSvc)

	// ── ClipSourceBuilder (Clip→Script + Catalog→Script) ───────────────
	wireScriptFlowExtras(scriptFlowHandler, core.OllamaClient, core.VectorSvc, core.ClipsOnlyRepo, engine, cfg, log)
	scriptFlowHandler.RegisterJobHandlers(jobServiceFacade)

	// ── Auto-Tagging Service ───────────────────────────────────────────
	autotagSvc := autotag.NewService(dbs.main.DB, core.AssetRepo, core.VLMClient, log)
	if core.ClipIndexerService != nil {
		autotagSvc.SetVectorStore(core.ClipIndexerService.VectorStore())
	}

	// ── Deletion Service ───────────────────────────────────────────────
	deletionSvc := media.NewDeletionService(
		core.ClipsOnlyRepo, core.ClipsOnlyRepo, core.ClipsOnlyRepo,
		mediaDomain.VoiceoverRepo, mediaDomain.ImageRepo,
		core.DriveUploader, core.AssetTreeService, core.AssetIndexService, log,
	)

	// ── Maintenance Service ────────────────────────────────────────────
	maintenanceSvc := maintenance.NewService(cfg, log,
		core.AssetIndexService, core.AssetTreeService, deletionSvc,
		jobsService, dbs.main.DB)
	if err := maintenanceSvc.RegisterHandler(); err != nil {
		log.Warn("failed to register maintenance handler", zap.Error(err))
	}

	// ── Lifecycle Scheduler ────────────────────────────────────────────
	lifecycleScheduler := scheduler.NewLifecycleScheduler(cfg, jobServiceFacade, log)
	concurrent.SafeGo("lifecycle-scheduler", func() { lifecycleScheduler.Start(ctx) })

	// ── Outbox Events Pool (PR5 — canonical outbox for async events) ───
	// The outbox_events Pool replaces the legacy media_index_outbox Worker.
	// It uses CTE-based atomic claim + lease fencing + retry/dead-letter.
	// The handler registry includes:
	//   - workflow.step.completed (audit log)
	//   - workflow.step.failed    (ERROR audit log + hookFn for alerting)
	//   - asset.index.requested   (real — calls clipIndexer.IndexClip)
	//   - delivery / metadata_export / provider_sync (stubs — return errors
	//     so events retry until dead_letter for operator visibility)
	outboxEventsRegistry := outboxevents.NewHandlerRegistry()
	// Wire the canonical real outbox handlers (delivery,
	// asset.metadata_export, provider.sync) plus workflow_step.* and the
	// optional IndexingHandler. Each handler is constructed with its
	// minimum-viable deps; nil ops are tolerated for unit-test wiring.
	// See internal/application/jobs/outbox/registry.go::Deps for the
	// dependency contract — introduced in the Operational Readiness PR.
	httpClient := &http.Client{Timeout: 30 * time.Second}

	// HMAC secrets for delivery.requested signing. The current secret
	// comes first so the verify path short-circuits on the common path;
	// the previous secret is kept around for the rotation window (per
	// dictate (1) in the Operational Readiness PR). The config layer's
	// Validate() enforces ≥32 bytes in production unless the dev escape
	// VELOX_ALLOW_INSECURE_DEV=true is set.
	var hmacSecrets [][]byte
	if cur := strings.TrimSpace(cfg.Security.DeliveryHMACSecret); cur != "" {
		hmacSecrets = append(hmacSecrets, []byte(cur))
	}
	if prev := strings.TrimSpace(cfg.Security.DeliveryHMACSecretPrevious); prev != "" {
		hmacSecrets = append(hmacSecrets, []byte(prev))
	}

	outboxDeps := &jobsoutbox.Deps{
		DB:          dbs.main.DB,
		HTTPClient:  httpClient,
		MetadataDir: cfg.Storage.FullPath("asset_metadata"),
		HMACSecrets: hmacSecrets,
		InsecureDev: cfg.Security.DeliveryInsecureDev,
		Jobs:        jobsService, // provider.sync dispatches onto jobs.Service for drive|youtube
	}
	if err := jobsoutbox.RegisterAll(outboxEventsRegistry, log, core.ClipIndexerService, outboxDeps); err != nil {
		log.Warn("failed to register outbox events handlers", zap.Error(err))
	}
	cfgPoll := 500 * time.Millisecond
	if cfg.Outbox.PollIntervalMs > 0 {
		cfgPoll = time.Duration(cfg.Outbox.PollIntervalMs) * time.Millisecond
	}
	cfgReclaim := 60 * time.Second
	if cfg.Outbox.ReclaimIntervalSeconds > 0 {
		cfgReclaim = time.Duration(cfg.Outbox.ReclaimIntervalSeconds) * time.Second
	}
	cfgProcess := 30 * time.Second
	if cfg.Outbox.ProcessTimeoutSeconds > 0 {
		cfgProcess = time.Duration(cfg.Outbox.ProcessTimeoutSeconds) * time.Second
	}
	outboxEventsCfg := outboxevents.WorkerPollConfig{
		PollInterval:    cfgPoll,
		ProcessTimeout:  cfgProcess,
		ReclaimInterval: cfgReclaim,
	}
	outboxEventsPool := outboxevents.NewPool("outbox-events", outboxEventsRepo, outboxEventsRegistry, log, outboxEventsCfg)
	concurrent.SafeGo("outbox-events-pool", func() {
		outboxEventsPool.Start(ctx, 1)
	})
	concurrent.SafeGo("outbox-events-shutdown", func() {
		<-ctx.Done()
		if err := outboxEventsPool.Stop(15 * time.Second); err != nil {
			log.Warn("outbox events pool stop returned error", zap.Error(err))
		}
	})
	log.Info("outbox events pool started for workflow.step.* + asset.index.requested + stubs",
		zap.Duration("poll_interval", outboxEventsCfg.PollInterval),
		zap.Duration("process_timeout", outboxEventsCfg.ProcessTimeout))

	// ── Lessons Service ────────────────────────────────────────────────
	lessonsSvc := lessons.NewService(
		&lessons.LessonsConfig{
			Enabled:             cfg.Lessons.Enabled,
			DefaultModel:        cfg.Lessons.DefaultModel,
			DefaultTone:         cfg.Lessons.DefaultTone,
			DefaultLanguage:     cfg.Lessons.DefaultLanguage,
			DefaultImageModel:   cfg.Lessons.DefaultImageModel,
			MaxParallelChapters: cfg.Lessons.MaxParallelChapters,
			OllamaURL:           cfg.External.OllamaURL,
		},
		core.ScriptGen, mediaDomain.ImageService, core.DocClient, log,
	)
	log.Info("Lessons service initialized", zap.Bool("enabled", cfg.Lessons.Enabled))
	lessonsSvc.RegisterJobHandler(jobsService)

	// ── Asset Satellite Repositories (canonical model completion, PR0) ────
	assetLocRepo := core.AssetsSvc.LocationRepository()
	assetProcRepo := core.AssetsSvc.ProcessingRepository()
	assetVerRepo := core.AssetsSvc.VersionRepository()

	// Wire asset lifecycle repos into YouTube service (late-binding).
	if mediaDomain.YoutubeClipService != nil {
		mediaDomain.YoutubeClipService.SetAssetRepos(assetProcRepo, assetVerRepo)
		log.Debug("asset lifecycle repos wired into youtube service")
	}

	// ── Asset Query Service (canonical aggregate reader) ────────────────
	assetsSvc := core.AssetsSvc
	log.Info("asset.Service wired (canonical aggregate reader)")

	return &services{
		scriptGen:          core.ScriptGen,
		docClient:          core.DocClient,
		driveUploader:      core.DriveUploader,
		driveClient:        core.DriveClient,
		driveDests:         core.DriveDests,
		utility:            common.NewUtilityHandler(),
		scriptsRepo:        mediaDomain.ScriptsRepo,
		imageRepo:          mediaDomain.ImageRepo,
		imageService:       mediaDomain.ImageService,
		clipsRepo:          core.ClipsOnlyRepo,
		assetRepo:          core.AssetRepo,
		monitorsRepo:       mediaDomain.MonitorsRepo,
		voiceoverService:   mediaDomain.VoiceoverService,
		voiceoverSync:      voiceoverSync,
		clipIndexerService: core.ClipIndexerService,
		catalogRepo:        catalogRepo,
		catalogSync:        catalogSync,
		assocService:       assocService,
		jobsRepo:           jobsRepo,
		jobsService:        jobsService,
		jobServiceFacade:   jobServiceFacade,
		jobsDispatcher:     jobsDispatcher,
		memoryRepo:         memoryRepo,
		mediaProcessor:     core.MediaProcessor,
		ollamaClient:       core.OllamaClient,
		youtubeClipService: mediaDomain.YoutubeClipService,
		assetIndexService:  core.AssetIndexService,
		assetTreeService:   core.AssetTreeService,
		assetResolver:      assetResolver,
		lifecycleScheduler: lifecycleScheduler,
		maintenanceSvc:     maintenanceSvc,
		styleRegistry:      core.StyleRegistry,
		vectorSvc:          core.VectorSvc,
		realtimeSvc:        realtimeSvc,
		vlmClient:          core.VLMClient,
		autotagService:     autotagSvc,
		booksService:       mediaDomain.BooksService,
		lessonsService:     lessonsSvc,
		mediaStore:         core.MediaStore,

		outboxDispatcher: outboxDispatcher,

		outboxEventsRepo:     outboxEventsRepo,
		outboxEventsPool:     outboxEventsPool,
		outboxEventsRegistry: outboxEventsRegistry,

		assetLocationsRepo:  assetLocRepo,
		assetProcessingRepo: assetProcRepo,
		assetVersionsRepo:   assetVerRepo,
		assetsSvc:           assetsSvc,
	}, nil
}
