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

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/autotag"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/hashutil"
	pkgffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	ytinfra "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/youtube"
	ytcache "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/youtube/cache"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ytdlp"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"github.com/Marcuss-ops/PipelineGen/pkg/portutil"
)

// BuildDomainBundle builds the media-domain services.
//
// Split-topology: companion files own the standalone bundle builders:
//   - build_bundles_ai.go: BuildAIBundle (Ollama/script-gen/translation)
//   - build_bundles_maint.go: BuildMaintBundle (deletion + maintenance)
//   - build_bundles_sync.go: BuildSyncBundle + buildYouTubeRuntimeConfig
//   - build_bundles_ingest.go: buildIngestService + imageListRepoAdapter
//   - buildImageSearchResolver
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
	// Phase 1d (July 2026): YouTube → Publisher migration.
	// The canonical YouTubePublisherDriveAdapter routes UploadFileIfChanged
	// through delivery.Publisher.Publish (with ConflictSkipByHash for
	// content-dedupe) instead of the legacy drive.Admin.UploadFileIfChanged.
	// The legacy driveFolderMgr adapter is retained as the fallback for now;
	// a future CUTOVER wave will retire it entirely.
	youtubePubAdapter := NewYouTubePublisherDriveAdapter(drive.Publisher, log)
	driveFolderMgr := newDriveFolderMgrAdapter(drive.driveUploader, log) // Phase 1d: retained as fallback; retire in CUTOVER wave
	_ = driveFolderMgr                                                   // suppress unused-var while both adapters coexist
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

	// Commit 1/6 (PR-C-YouTube-Cutover, June 2026): construct the
	// ClipCacheAdapter + ClipAtomicWriterAdapter pair (the bare DB
	// halves of the canonical 2-port surface) and the
	// ProcessYouTubeSegmentUseCase that consumes them.
	//
	// Subtitles / Transcriber are intentionally NOT wired here —
	// the use case's runtime path tolerates nil on both (steps 6 + 7
	// are gated by `if u.deps.Subtitles != nil` / `if u.deps.Transcriber != nil`
	// in process_segment.go). A future commit wires YTDLPSubtitleAdapter
	// for Subtitles and the canonical Whisper bridge for Transcriber;
	// for Commit 1 the post-cut step degrades to a no-op path.
	//
	// PR-WIRE-SUBTITLE-FETCHER-ADAPTER (2026-07-06): construct the
	// infrastructure-layer SubtitleFetcherAdapter (post
	// PR-SUBTITLES-BASEARGS-MIGRATION the canonical Pattern-0 port
	// consumer) so application/youtube/usecase.Service.SliceSubtitles
	// stops silently erroring at runtime with `youtube: subtitle
	// fetcher port not wired`. The adapter satisfies the
	// application-side SubtitleFetcherPort (internal/application/youtube/ports/ports.go:160)
	// structurally via Go's implicit interface satisfaction (it
	// implements the SliceSubtitles method). UseCookies=false here
	// because the YouTube segmentation path is invoked for public
	// auto-generated content (n-challenge / age-restricted videos
	// route through the monitor's YTDLPSubtitleAdapter, not the
	// YouTube use case's SliceSubtitles). The cacheDir is the
	// canonical cfg.Storage.SubtitlesPath() (added in this PR for
	// godlike/06 SSOT consistency with VoiceoversPath/AssetsPath/etc).
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
	// Commit 4/6 (PR-C-YouTube-Cutover, June 2026, P1 #15 + #16):
	// the canonical ClipMetadataWriter adapter (tx-bound UPDATE
	// media_assets + INSERT outbox_events). Wired BEFORE the
	// Ollama builder so the metadata service construction can
	// reference the writer.
	clipMetadataWriter := assets.NewClipMetadataWriterAdapter(dbs.main.DB, outbox.EventsRepo, log)
	// Commit 4/6: concrete Ollama ClipMetadataBuilder with
	// deterministic fallback. The model name is read from the
	// canonical cfg.External.OllamaMetadataModel (set at
	// buildYouTubeRuntimeConfig); empty value falls through to
	// the client's default model. The deterministic fallback
	// uses the formula in metadata/service.go so the production
	// + fallback score ranges are identical.
	ollamaBuilder := ytinfra.NewOllamaClipMetadataBuilder(
		ai.OllamaClient,
		buildYouTubeRuntimeConfig(cfg).OllamaMetadataModel,
		0, // timeout=0 → default 60s
		log,
	)
	// Commit 4/6: the canonical metadata service. Wired into
	// DomainBundle.ClipMetadataService for the late-bindings
	// block; not yet threaded into the existing
	// usecase.MetadataService (that migration is a future
	// PR-4.7 follow-up; the spec is satisfied by the new
	// service being canonical).
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
	processSeg := youtube.NewProcessYouTubeSegmentUseCase(youtube.ProcessSegmentDeps{
		Cache:          clipCache,
		VideoPipeline:  videoPipelineAdapter,
		Hash:           hashAdapter,
		DriveFolderMgr: youtubePubAdapter, // Phase 1d: canonical Publisher.Publish path
		Writer:         clipWriter,
		// Commit 4/6 (PR-C-YouTube-Cutover, P1 #15 + #16):
		// the ClipMetadataWriter is wired as an optional port
		// (no panic on nil at ctor time). When non-nil, Step 10
		// of the pipeline writes the CanonicalClipMetadata to
		// media_assets + emits the metadata outbox event. When
		// nil, the pipeline short-circuits Step 10 silently
		// (the clip write alone is sufficient for the indexing
		// path; the metadata write is a downstream enrichment).
		ClipMetadataWriter: clipMetadataWriter,
		MetadataService:    clipMetadataService,
		SegmentsSvc:        youtube.NewSegmentsService(), // dependency-free helper, safe at boot
		// Commit 2/6 (PR-C-YouTube-Cutover, Correttezza #3):
		// SegmentPolicy is the duration gate applied to every
		// segment (LLM-discovered and API-supplied). Default
		// Min=4s / Max=60s is the user-requested clip-duration
		// policy (no effects, no transitions applied by the
		// YouTube extraction endpoint — only cut + audio + Drive
		// upload + media_assets write + Qdrant indexing event).
		// A future config-driven value can flow through here via
		// cfg.YouTube.MinSegmentDuration / MaxSegmentDuration.
		SegmentPolicy: youtubetypes.DefaultSegmentPolicy(),
		Log:           log,
	})

	youtubeDeps := youtube.ServiceDeps{
		Cfg:               buildYouTubeRuntimeConfig(cfg),
		Log:               log,
		MediaProcessor:    process.MediaProcessor,
		VideoPipeline:     videoPipelineAdapter,
		LifecycleService:  youtubeLifecycle, // youtube's lifecycle (NOT the retired PR-ARTLIST-LIFECYCLE artlist forward-pointer, 2026-07-04)
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
		// Commit 1/6: ProcessSeg is wired from the canonical use case
		// constructed above. Wired into NewExtractionService via
		// NewService so Extract fans out through the 9-step
		// pipeline + ClipAtomicWriter.CommitClipAndIndexEvent.
		ProcessSeg:       processSeg,
		TranscriptReader: &youtube.OSTranscriptReader{},
		// PR-WIRE-SUBTITLE-FETCHER-ADAPTER (2026-07-06): wire the
		// infrastructure-layer SubtitleFetcherAdapter as the
		// application-side SubtitleFetcherPort. The port is
		// optional (service_validate_test.go:130 sets it nil to
		// verify the validator tolerates missing optional ports);
		// pre-PR wiring left it nil so callbacks.go:110-112
		// surfaced a runtime "youtube: subtitle fetcher port not
		// wired" error. Post-PR the Service.SliceSubtitles
		// callback routes through the adapter, which delegates
		// the canonical yt-dlp argv prefix to ytdlp.BaseArgs
		// (same Pattern-0 port the monitor's YTDLPSubtitleAdapter
		// uses post PR-WIRE-SUBTITLE-FETCHER-ADAPTER migration).
		SubtitleFetcher: subtitleFetcherAdapter,
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
	// Step 8/12 (June 2026, child use case on the new 7-port boundary):
	// buildVoiceoverService now returns a 3rd value — the canonical
	// *voiceover.ProcessVoiceoverItemUseCase constructed on top of the
	// 7-port typed seam (Pattern 0). The use case is the canonical
	// per-item pipeline that GenerateItemJobHandler dispatches when
	// voiceover.generate_item jobs arrive (replacing the legacy
	// ProcessOneVoiceoverUseCase bridge for Step 12 retirement).
	voiceoverSvc, voiceoverRepo, voiceoverProcessItem, audioProcessor := buildVoiceoverService(ctx, cfg, dbs, log,
		drive.driveUploader,
		drive.Publisher,
		search.AssetIndexService, process.ClipIndexerService,
		drive.DestResolver,
		voMetaWriter, ai.ScriptGen,
		outbox.Dispatcher,
	)

	// F2.10 (June 2026): the legacy driveUploader arg was retired (override
	// brutal) — books uploads route through delivery.Publisher, books downloads
	// route through the canonical drive.Reader port (concrete *drive.Uploader
	// satisfies drive.Reader structurally per the compile-time assertion at
	// internal/infrastructure/drive/ports.go so the field passes at compile).
	//
	// Fase 7 review-fix #2 (July 2026): composition-root hard-fail on the
	// books.BookTransformer construction. buildBooksService signature
	// changed from `*books.Service` to `(*books.Service, error)` so a
	// transformer-construction failure aborts NewComposition rather than
	// silently returning a half-wired Service (godlike/07 §"No fake
	// availability" closure). The error taxonomy matches the surrounding
	// pattern (`compose domains: <surface>: %w`, mirrors `compose domains:
	// youtube SearchRunnerPort typed-nil` + `compose domains: clip
	// metadata service` + `compose domains: outbox.Dispatcher is required`).
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

	// P1-6 (July 2026): drive.DocClient is an interface value whose
	// concrete (*drive.DocClientImpl) now satisfies delivery.DocPublisher
	// (compile-time assertion in delivery/doc_publisher.go). Go cannot
	// implicitly convert between two interface types with identical method
	// sets, so we type-assert here. The assertion is safe because
	// NewDocClient always returns *DocClientImpl, and the compile-time
	// pin var _ delivery.DocPublisher = (*drive.DocClientImpl)(nil) locks
	// the conformance at build time.
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

	// Step 8/12 (June 2026): the canonical per-item use case is wired
	// here so the late-bindings block in composition.go can construct
	// GenerateItemJobHandler on top of the typed VoiceoverItemExecutor
	// port (Pattern 0, AGENTS.md). The previous forward-deferred comment
	// (BLOC5.4) is closed by this assignment.

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

	// FASE 7 (July 2026, image-territories action plan): wire the
	// routing.ImageSearchResolver from imageSvc.RetrievalRegistry() +
	// repos.ImageRepo. Fail-closed per godlike/07; if either input is
	// missing the composition error aborts NewComposition (no
	// half-wired resolver on DomainBundle).
	imageSearchResolver, err := buildImageSearchResolver(imageSvc, repos.ImageRepo, log)
	if err != nil {
		return nil, fmt.Errorf("compose images: %w", err)
	}

	return &DomainBundle{
		YoutubeClipService:           youtubeClipService,
		VoiceoverService:             voiceoverSvc,
		VoiceoverSync:                vosyncSvc,
		VoiceoverProcessItem:         voiceoverProcessItem, // Step 8/12: narrow VoiceoverItemExecutor port (BLOC5.4 closer)
		ImageService:                 imageSvc,
		IngestService:                ingestSvc,
		BooksService:                 booksSvc,
		LessonsService:               lessonsS,
		MetaWriter:                   metaWriter,
		RealtimeMatcher:              realtimeMatcher,
		RealtimeSearch:               realtimeSearch,
		AutotagService:               autotagSvc,
		AssocService:                 assocService,
		ArtifactService:              artifactService,
		VoiceoverGenerateItemHandler: nil, // populated at composition.go late-bindings block
		// Commit 4/6: the canonical metadata service. Populated
		// in BuildDomainBundle; consumed by the late-bindings
		// block + future EnrichClip migration (PR-4.7).
		ImageSearchResolver: imageSearchResolver, // FASE 7
		AudioProcessor:      audioProcessor,      // VO-DECOMPOSITION P0 #1: persistent TTS worker
	}, nil
}
