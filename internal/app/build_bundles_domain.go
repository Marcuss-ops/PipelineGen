package app

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/videomuscles"
	lessonsSvc "github.com/Marcuss-ops/PipelineGen/internal/application/lessons"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"

	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	voiceoverreconcile "github.com/Marcuss-ops/PipelineGen/internal/application/assets/reconciliation/voiceover"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	ytmetadata "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/metadata"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"

	"github.com/Marcuss-ops/PipelineGen/internal/application/transcripts"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/autotag"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/hashutil"
	pkgffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	ytinfra "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/youtube"
	ytcache "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/youtube/cache"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ytdlp"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"github.com/Marcuss-ops/PipelineGen/pkg/portutil"
)

// BuildDomainBundle builds the media-domain services.
// Requires outbox.Dispatcher (injected via OutboxBundle, last arg).
func BuildDomainBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, drive *DriveBundle, repos *RepoBundle, search *SearchBundle, process *ProcessBundle, ai *AIBundle, outbox *OutboxBundle) (*DomainBundle, error) {
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
		Publisher:   drive.Publisher,
		DriveReader: drive.driveUploader,
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
	youtubePubAdapter := NewYouTubePublisherDriveAdapter(drive.Publisher, log)
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

	// UseCookies=false: public video segmentation (n-challenge path via monitor).
	subtitleFetcherAdapter := ytinfra.NewSubtitleFetcherAdapter(
		ytinfra.SubtitleCacheConfig{
			YTDLPPath:    cfg.External.ResolvedYtdlpPath(),
			DefaultLangs: "en,en-US",
			CacheDir:     cfg.Storage.SubtitlesPath(),
		},
		nil, // runner=nil → NewProcessRunnerAdapter() per the nil-fallback in the ctor
		ytdlp.NewCommandBuilder(cfg),
		false, // UseCookies=false (public segmentation; n-challenge path is via the monitor)
	)
	clipCache := assets.NewClipCacheAdapter(repos.ClipsRepo, log)
	clipWriter := assets.NewClipAtomicWriterAdapter(dbs.main.DB, outbox.EventsRepo, log)
	clipMetadataWriter := assets.NewClipMetadataWriterAdapter(dbs.main.DB, outbox.EventsRepo, log)
	ollamaBuilder := ytinfra.NewOllamaClipMetadataBuilder(
		ai.OllamaClient,
		buildYouTubeRuntimeConfig(cfg).OllamaMetadataModel,
		0, // timeout=0 → default 60s
		log,
	)
	clipMetadataService, err := ytmetadata.NewMetadataService(ytmetadata.MetadataDeps{
		Builder:  ollamaBuilder,
		Writer:   clipMetadataWriter,
		Logger:   log,
		JobID:    "",
		JobGroup: "general",
	})
	if err != nil {
		return nil, fmt.Errorf("compose domains: clip metadata service: %w", err)
	}

	// Compile-time pin: Step10MetricsRecorder port ↔ Step10MetricsAdapter.
	var _ youtubeports.Step10MetricsRecorder = (*observability.Step10MetricsAdapter)(nil)
	processSeg := youtube.NewProcessYouTubeSegmentUseCase(youtube.ProcessSegmentDeps{
		Cache:              clipCache,
		VideoPipeline:      videoPipelineAdapter,
		Hash:               hashAdapter,
		DriveFolderMgr:     youtubePubAdapter, // Phase 1d: canonical Publisher.Publish path
		Writer:             clipWriter,
		ClipMetadataWriter: clipMetadataWriter,
		MetadataService:    clipMetadataService,
		SegmentsSvc:        youtube.NewSegmentsService(),        // dependency-free helper, safe at boot
		SegmentPolicy:      youtubetypes.DefaultSegmentPolicy(), // default Min=4s / Max=60s
		Step10Metrics:      observability.NewStep10MetricsAdapter(),
		Log:                log,
	})

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
		DriveFolderMgr:    youtubePubAdapter, // Phase 1d: canonical Publisher.Publish path
		FolderMemory:      newFolderMemoryAdapter(folderMemSvc),
		SearchRunner:      searchRunnerAdapter,
		HashSvc:           hashAdapter,
		ProcessSeg:        processSeg,
		TranscriptReader:  &youtube.OSTranscriptReader{},
		SubtitleFetcher:   subtitleFetcherAdapter,
	}
	if err := youtube.ValidateServiceDeps(youtubeDeps); err != nil {
		return nil, fmt.Errorf("compose youtube: %w", err)
	}
	youtubeClipService := youtube.NewService(youtubeDeps)

	if outbox == nil || outbox.Dispatcher == nil {
		return nil, fmt.Errorf("compose domains: outbox.Dispatcher is required (PR-VO-A3 voiceover indexing handoff)")
	}
	voiceoverSvc, voiceoverRepo, voiceoverProcessItem, audioProcessor := buildVoiceoverService(ctx, cfg, dbs, log,
		drive.driveUploader,
		drive.Publisher,
		search.AssetIndexService, process.ClipIndexerService,
		drive.DestResolver,
		voMetaWriter, ai.OllamaTranslator, // satisfies translation.TranslationPort
		outbox.Dispatcher,
	)

	booksSvc, err := buildBooksService(cfg, dbs, log, voiceoverSvc, drive.Publisher, drive.driveUploader)
	if err != nil {
		return nil, fmt.Errorf("compose domains: books transformer: %w", err)
	}

	ingestSvc := buildIngestService(cfg, log, dbs, drive.driveUploader, drive.Publisher, repos, search, mutationsDisp)

	imageSvc, metaWriter := buildImagesService(ctx, cfg, log,
		drive.driveUploader, repos.ClipsRepo, repos.ClipsRepo,
		drive.StyleRegistry, ai.ScriptGen,
		drive.MediaStore, drive.Publisher, repos.ImageRepo,
		voMetaWriter, ingestSvc,
		outbox.Dispatcher,
	)

	var realtimeMatcher assetsapi.RealtimeMatcher
	var realtimeSearch usecase.RealtimeSearchService

	autotagSvc := autotag.NewService(dbs.main.DB, repos.Assets.Repository(), process.VLMClient, nil, log)

	var assocService usecase.AssocSearchService
	log.Info("association service unavailable — package removed from remote")

	// drive.DocClient (*drive.DocClientImpl) satisfies delivery.DocPublisher;
	// compile-time pin in delivery/doc_publisher.go locks conformance.
	docPublisher, ok := drive.DocClient.(delivery.DocPublisher)
	if !ok {
		return nil, fmt.Errorf("compose domains: lessons: drive.DocClient does not satisfy delivery.DocPublisher (P1-6 migration incomplete)")
	}
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
		ai.ScriptGen, imageSvc, docPublisher, log,
	)
	log.Info("Lessons service initialized", zap.Bool("enabled", cfg.Lessons.Enabled))

	var vosyncSvc *voiceoverreconcile.Service
	if voFolder := cfg.Drive.VoiceoverFolder(); voFolder != "" && voiceoverRepo != nil {
		vosyncSvc = voiceoverreconcile.NewService(drive.driveUploader, voiceoverRepo, search.AssetTreeService, voFolder, log)
		log.Info("Voiceover sync service initialized", zap.String("root_folder_id", voFolder))
	}

	artifactBlobStore, err := artifacts.NewLocalBlobStore(cfg.Storage.DataDir)
	if err != nil {
		return nil, fmt.Errorf("compose domains: artifact blob store: %w", err)
	}
	artifactRepo := artifacts.NewSQLiteRepository(dbs.main.DB)
	artifactService := artifacts.NewService(artifactBlobStore, artifactRepo, log)
	log.Info("P0.1: artifact blob service wired (content-addressed staging + verify + promote)",
		zap.String("data_dir", cfg.Storage.DataDir))

	imageSearchResolver, err := buildImageSearchResolver(imageSvc, repos.ImageRepo, log)
	if err != nil {
		return nil, fmt.Errorf("compose images: %w", err)
	}

	extractDl := downloader.NewYTDLP(cfg)
	extractAdapters := BuildExtractImportantClipsAdapters(ExtractImportantClipsAdapterDeps{
		Subtitles: transcripts.NewYTDLPSubtitleAdapter(transcripts.Deps{
			Ytdlp:      extractDl,
			CmdBuilder: ytdlp.NewCommandBuilder(cfg),
			UseCookies: false, // gemma is public video segmentation; monitor uses separate UseCookies=true instance
			Log:        log,
		}),
		Downloader: extractDl,
		Folder:     &adminFolderManagerAdapter{admin: drive.Admin},
		Files: func(ctx context.Context, req drivePutFnRequest) (*drivePutFnResult, error) {
			if drive.driveUploader == nil {
				return nil, fmt.Errorf("compose domains: extract-important upload: drive.driveUploader unwired")
			}
			// drive.Admin.UploadFile — forward-pointer PR-GEMMA-EXTRACT-IMPORTANT-FOLLOWUP
			// migrates to delivery.Publisher.Publish (Pattern 0 canonical write seam).
			res, err := drive.driveUploader.UploadFile(ctx, req.LocalPath, req.FolderID, req.Filename)
			if err != nil {
				return nil, fmt.Errorf("compose domains: extract-important upload: %w", err)
			}
			return &drivePutFnResult{FileID: res.FileID, WebViewLink: res.WebViewLink}, nil
		},
	})
	extractUseCase := youtube.NewExtractImportantClipsUseCase(
		log,
		extractAdapters.TranscriptFetcher,
		nil, // analyzer=nil → failClosedAnalyzerAdapter returns ErrAnalyzerUnavailable at runtime
		extractAdapters.SectionDownloader,
		extractAdapters.DriveFolder,
		extractAdapters.DriveUploader,
		clipWriter, // canonical ClipAtomicWriter constructed earlier this fn
		extractAdapters.Hasher,
	)
	extractHandler := youtube.NewExtractImportantClipsJobHandler(extractUseCase, log)

	return &DomainBundle{
		YoutubeClipService:              youtubeClipService,
		ExtractImportantClipsJobHandler: extractHandler,
		VoiceoverService:                voiceoverSvc,
		VoiceoverSync:                   vosyncSvc,
		VoiceoverProcessItem:            voiceoverProcessItem,
		ImageService:                    imageSvc,
		IngestService:                   ingestSvc,
		BooksService:                    booksSvc,
		LessonsService:                  lessonsS,
		MetaWriter:                      metaWriter,
		RealtimeMatcher:                 realtimeMatcher,
		RealtimeSearch:                  realtimeSearch,
		AutotagService:                  autotagSvc,
		AssocService:                    assocService,
		ArtifactService:                 artifactService,
		VoiceoverGenerateItemHandler:    nil, // populated at composition.go late-bindings block

		ImageSearchResolver: imageSearchResolver, // FASE 7
		AudioProcessor:      audioProcessor,      // VO-DECOMPOSITION P0 #1: persistent TTS worker
	}, nil
}
