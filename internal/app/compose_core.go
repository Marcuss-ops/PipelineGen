package app

import (
	"context"
	"os"
	"time"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/internal/config"
	"github.com/Marcuss-ops/PipelineGen/internal/core/destination"
	"github.com/Marcuss-ops/PipelineGen/internal/core/processor"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/media/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/media/realtime"
	"github.com/Marcuss-ops/PipelineGen/internal/media/storage"
	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	"github.com/Marcuss-ops/PipelineGen/internal/ml/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/ml/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/reranker"
	"github.com/Marcuss-ops/PipelineGen/internal/service/translations"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/vlm"
)

// CoreInfra holds core infrastructure services produced by composeCoreInfra.
type CoreInfra struct {
	OllamaClient  *client.Client
	ScriptGen     *ollama.Generator
	DocClient     drive.DocClient
	DriveClient   *gdrive.Service
	DriveUploader *drive.Uploader
	StyleRegistry *generation.StyleRegistry
	DriveDests    *DriveDestinations // resolved Drive folder IDs (immutable Config)

	ClipsOnlyRepo      *clips.Repository
	MediaProcessor     processor.Processor
	AssetIndexService  *assetindex.Service
	AssetTreeService   *assettree.Service
	ClipIndexerService *clipindexer.Service
	VLMClient          *vlm.Client
	VectorSvc          *vectorstore.Service
	MediaStore         *storage.Store
	DestResolver       destination.Resolver
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
	translationCache := translations.NewCache(dbs.main.DB)
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
	clipsOnlyRepo := clips.NewRepository(dbs.main.DB, log)
	mediaProcessor := initMediaProcessor(cfg, clipsOnlyRepo, log, driveUploader)

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

	storageResolver := storage.NewResolver(
		storage.MediaRoot(cfg.Storage.MediaPath()),
		storage.DriveRoot(dests.RootFolder()),
	)
	mediaStore := storage.NewStore(storageResolver, driveUploader, dests.RootFolder(), dests.ImagesFolder(), dests.VideoAIRoot, dests.SoundEffectsRoot, log)
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
	destResolver := storage.NewDestinationResolver(mediaStore)

	return &CoreInfra{
		OllamaClient:  ollamaClient,
		ScriptGen:     scriptGen,
		DocClient:     docClient,
		DriveClient:   driveClient,
		DriveUploader: driveUploader,
		StyleRegistry: styleRegistry,
		DriveDests:    dests,

		ClipsOnlyRepo:      clipsOnlyRepo,
		MediaProcessor:     mediaProcessor,
		AssetIndexService:  assetIndexService,
		AssetTreeService:   assetTreeService,
		ClipIndexerService: clipIndexerService,
		VLMClient:          vlmClient,
		VectorSvc:          vectorSvc,
		MediaStore:         mediaStore,
		DestResolver:       destResolver,
	}, nil
}

// composeRealtimeService creates the real-time matching service when enabled.
//
// PR3-5b.4: clips (media_assets canonical metadata) and the
// outboxevents.Repository (outbox_events counters) are threaded explicitly so
// realtime.IndexHealth can run the canonical sqlite<->qdrant cross-check.
// Both are optional — the service logs a WARN at startup if missing and the
// cross-check falls back to zeros — but production wiring MUST pass non-nil.
func composeRealtimeService(ctx context.Context, cfg *config.Config, log *zap.Logger, vectorSvc *vectorstore.Service, clipsRepo *clips.Repository, outboxEventsRepo *outboxevents.Repository, jobsService *jobservice.Service) *realtime.Service {
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
