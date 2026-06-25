package app

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/videomuscles"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	lessonsSvc "github.com/Marcuss-ops/PipelineGen/internal/application/lessons"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	voiceoversync "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/sync"
	"github.com/Marcuss-ops/PipelineGen/internal/application/youtube"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/autotag"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	pkgffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	ytinfra "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/youtube"
	ytcache "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/youtube/cache"

	"github.com/Marcuss-ops/PipelineGen/pkg/portutil"
)

// BuildDomainBundle builds the media-domain services.
func BuildDomainBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, drive *DriveBundle, repos *RepoBundle, search *SearchBundle, process *ProcessBundle, ai *AIBundle) (*DomainBundle, error) {
	clipsRegistry := artifacts.NewClipsRegistry(
		dbs.main.DB,
		repos.Assets.Repository(),
		repos.Assets,
		repos.Assets.LocationRepository(),
		repos.Assets.ProcessingRepository(),
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

	voiceoverSvc, voiceoverRepo := initVoiceoverService(ctx, cfg, dbs, log,
		drive.DriveClient, drive.DriveUploader,
		search.AssetIndexService, process.ClipIndexerService,
		drive.DestResolver,
		voMetaWriter, ai.ScriptGen,
	)

	booksSvc := initBooksService(cfg, dbs, log, drive.DriveUploader, voiceoverSvc)

	ingestSvc := buildIngestService(cfg, log, dbs, drive.DriveClient, repos, search)

	imageSvc, metaWriter := initImageService(ctx, cfg, log,
		drive.DriveClient, repos.ClipsRepo, repos.ClipsRepo,
		drive.StyleRegistry, ai.ScriptGen,
		drive.MediaStore, repos.ImageRepo,
		voMetaWriter, ingestSvc,
	)

	var realtimeSvc interface{} = nil

	// PG-034 (June 2026): autotagVectorStore removed — Qdrant capability
	// deleted. The autotag service no longer takes a vector-store indexer;
	// its semantic-tagging pipeline can still consume clip embeddings from
	// the DB but no longer propagates them to a vector store backend.
	autotagSvc := autotag.NewService(dbs.main.DB, repos.Assets.Repository(), process.VLMClient, nil, log)

	var assocService interface{} = nil
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
		RealtimeService:    realtimeSvc,
		AutotagService:     autotagSvc,
		AssocService:       assocService,
	}, nil
}

// buildIngestService constructs the ingest.Service from the same deps
// that WireMediaIngest uses.
func buildIngestService(cfg *config.Config, log *zap.Logger, dbs *databases, driveClient *gdrive.Service, repos *RepoBundle, search *SearchBundle) *ingest.Service {
	if driveClient == nil {
		return nil
	}
	if repos.ImageRepo == nil || repos.VoiceoverRepo == nil || repos.ClipsRepo == nil || search.AssetIndexService == nil {
		return nil
	}
	imagesRegistry := imgservice.NewRegistryAdapter(repos.ImageRepo, cfg.Storage.ImagesPath(), log)
	imagesLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: imagesRegistry, DriveClient: driveClient, AssetIndex: search.AssetIndexService, Store: ingest.NewImageStoreAdapter(repos.ImageRepo, cfg.Storage.ImagesPath())}, log)
	voiceoverRegistry := voiceover.NewVoiceoverRegistryAdapter(repos.VoiceoverRepo)
	voiceoverLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: voiceoverRegistry, DriveClient: driveClient, AssetIndex: search.AssetIndexService, Store: ingest.NewVoiceoverStoreAdapter(repos.VoiceoverRepo)}, log)
	clipRegistry := artifacts.NewClipsRegistry(dbs.main.DB, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository())
	clipLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: clipRegistry, DriveClient: driveClient, AssetIndex: search.AssetIndexService, Store: ingest.NewClipStoreAdapter(dbs.main.DB, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository())}, log)
	stockRegistry := artifacts.NewClipsRegistry(dbs.main.DB, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository())
	stockLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: stockRegistry, DriveClient: driveClient, AssetIndex: search.AssetIndexService, Store: ingest.NewClipStoreAdapter(dbs.main.DB, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository())}, log)
	return ingest.NewService(cfg, log, driveClient, map[ingest.Kind]*ingest.Pipeline{
		ingest.KindImage:     {Kind: ingest.KindImage, DefaultSource: "image", RootFolderID: cfg.Drive.ImagesFolder(), Lifecycle: imagesLifecycle},
		ingest.KindVoiceover: {Kind: ingest.KindVoiceover, DefaultSource: "voiceover", RootFolderID: cfg.Drive.VoiceoverFolder(), Lifecycle: voiceoverLifecycle},
		ingest.KindClip:      {Kind: ingest.KindClip, DefaultSource: "youtube", RootFolderID: cfg.Drive.ClipsFolder(), Lifecycle: clipLifecycle},
		ingest.KindStock:     {Kind: ingest.KindStock, DefaultSource: "stock", RootFolderID: cfg.Drive.StockFolder(), Lifecycle: stockLifecycle},
	})
}
