package app

import (
	"context"
	"fmt"

	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/videomuscles"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/routing"
	lessonsSvc "github.com/Marcuss-ops/PipelineGen/internal/application/lessons"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	translation "github.com/Marcuss-ops/PipelineGen/internal/application/translation"

	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	voiceoverreconcile "github.com/Marcuss-ops/PipelineGen/internal/application/assets/reconciliation/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	ytmetadata "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/metadata"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/autotag"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
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
	driveFolderMgr := newDriveFolderMgrAdapter(drive.driveUploader, log)
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
	clipCache := assets.NewClipCacheAdapter(repos.ClipsRepo)
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
		DriveFolderMgr: driveFolderMgr,
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
		// Min=2s / Max=60s matches the legacy extraction block
		// (the verdict's P1 #9 explicit numeric choice). A
		// future config-driven value can flow through here via
		// cfg.YouTube.MinSegmentDuration / MaxSegmentDuration.
		SegmentPolicy: youtubetypes.DefaultSegmentPolicy(),
		Log:           log,
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
		DriveFolderMgr:    driveFolderMgr,
		FolderMemory:      newFolderMemoryAdapter(folderMemSvc),
		SearchRunner:      searchRunnerAdapter,
		HashSvc:           hashAdapter,
		// Commit 1/6: ProcessSeg is wired from the canonical use case
		// constructed above. Wired into NewExtractionService via
		// NewService so Extract fans out through the 9-step
		// pipeline + ClipAtomicWriter.CommitClipAndIndexEvent.
		ProcessSeg: processSeg,
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
	voiceoverSvc, voiceoverRepo, voiceoverProcessItem := buildVoiceoverService(ctx, cfg, dbs, log,
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
	}, nil
}

// imageListRepoAdapter bridges *assets.ImagesRepository (the infra
// producer of routing.RepositoryListFilter + []routing.RepositoryImageRow)
// to the canonical routing.ImageListRepository interface expected by
// the FASE 7 ImageSearchResolver (which accepts routing.ImageFilter +
// []routing.ImageSearchResult). The two structs are structurally
// identical — different package names only — so the adapter does a
// field-for-field rebind with no data loss.
type imageListRepoAdapter struct {
	repo *assets.ImagesRepository
}

// ListImages satisfies routing.ImageListRepository. Repository-only
// fields (Subject/Slug/Description/Tags/CreatedAt on the row) are
// dropped since the canonical ImageSearchResult does not expose
// them; field-for-field bind for the rest.
func (a *imageListRepoAdapter) ListImages(ctx context.Context, filter routing.ImageFilter) ([]routing.ImageSearchResult, error) {
	if a == nil || a.repo == nil {
		return nil, nil
	}
	// FASE 7 image-territories cleanup (July 2026, godlike/06 SSOT):
	// routing.ImageOrigin is a Go 1.9+ type alias for
	// asset.ImageOrigin (declared in internal/domain/asset/image_taxonomy.go).
	// Same type identity → []routing.ImageOrigin flows directly into
	// the []asset.ImageOrigin slot on routing.RepositoryListFilter
	// without conversion. The previous element-by-element cast loop
	// (a third knowledge site that the code-reviewer flagged) is
	// collapsed — there is no conversion at all.
	dbRows, err := a.repo.ListImages(ctx, routing.RepositoryListFilter{
		SubjectID: filter.SubjectID,
		Origins:   filter.Origins,
		Providers: filter.Providers,
		StyleIDs:  filter.StyleIDs,
		Tags:      filter.Tags,
		Limit:     filter.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]routing.ImageSearchResult, 0, len(dbRows))
	for _, r := range dbRows {
		out = append(out, routing.ImageSearchResult{
			AssetID:      r.AssetID,
			Origin:       r.Origin, // routing.ImageOrigin alias = asset.ImageOrigin, no conversion
			Provider:     r.Provider,
			Name:         r.Name,
			PreviewURL:   r.PreviewURL,
			Width:        r.Width,
			Height:       r.Height,
			Score:        r.Score,
			StyleID:      r.StyleID,
			StyleVersion: r.StyleVersion,
			License:      r.License,
		})
	}
	return out, nil
}

// Compile-time assertion: imageListRepoAdapter satisfies the
// canonical routing ImageListRepository.
var _ routing.ImageListRepository = (*imageListRepoAdapter)(nil)

// buildImageSearchResolver wires the FASE 7 routing layer
// (routing.ImageSearchResolver) from the canonical image-side deps.
// Fail-closed (godlike/07): if either input is nil we surface the
// composition error so NewComposition aborts rather than silently
// mounting a half-wired resolver.
func buildImageSearchResolver(imageSvc *imgservice.Service, imageRepo *assets.ImagesRepository, log *zap.Logger) (routing.ImageSearchResolver, error) {
	if imageSvc == nil || imageSvc.RetrievalRegistry() == nil {
		return nil, fmt.Errorf("routing.NewImageSearchResolver: retrieval backend is nil \u2014 image service must be constructed first")
	}
	if imageRepo == nil {
		return nil, fmt.Errorf("routing.NewImageSearchResolver: image list repository is nil \u2014 repos.ImageRepo required")
	}
	resolver, err := routing.NewImageSearchResolver(
		routing.WithRetrievalBackend(imageSvc.RetrievalRegistry()),
		routing.WithImageListRepository(&imageListRepoAdapter{repo: imageRepo}),
	)
	if err != nil {
		return nil, fmt.Errorf("routing.NewImageSearchResolver: %w", err)
	}
	log.Info("FASE 7: ImageSearchResolver wired (retrieval backend + image list repo)")
	return resolver, nil
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
func buildIngestService(cfg *config.Config, log *zap.Logger, dbs *databases, driveUploader *driveutil.Uploader, publisher delivery.Publisher, repos *RepoBundle, search *SearchBundle, mutationsDisp mutations.AssetMutationDispatcher) *ingest.Service {
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
	imagesLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: imagesRegistry, Publisher: publisher, DriveReader: driveUploader, AssetIndex: search.AssetIndexService, Store: ingest.NewImageStoreAdapter(repos.ImageRepo, cfg.Storage.ImagesPath())}, log)
	voiceoverRegistry := voiceover.NewVoiceoverRegistryAdapter(repos.VoiceoverRepo)
	voiceoverLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: voiceoverRegistry, Publisher: publisher, DriveReader: driveUploader, AssetIndex: search.AssetIndexService, Store: ingest.NewVoiceoverStoreAdapter(repos.VoiceoverRepo)}, log)
	clipRegistry := artifacts.NewClipsRegistry(dbs.main.DB, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(), mutationsDisp)
	clipLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: clipRegistry, Publisher: publisher, DriveReader: driveUploader, AssetIndex: search.AssetIndexService, Store: ingest.NewClipStoreAdapter(dbs.main.DB, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(), mutationsDisp)}, log)
	stockRegistry := artifacts.NewClipsRegistry(dbs.main.DB, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(), mutationsDisp)
	stockLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: stockRegistry, Publisher: publisher, DriveReader: driveUploader, AssetIndex: search.AssetIndexService, Store: ingest.NewClipStoreAdapter(dbs.main.DB, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(), mutationsDisp)}, log)
	return ingest.NewService(cfg, log, driveUploader.Admin(), map[ingest.Kind]*ingest.Pipeline{
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

	// Fase 9 step 2 (Spina Dorsale, July 2026): construct the
	// canonical OllamaTranslator — the single application-layer
	// concrete that satisfies translation.TranslationPort + the
	// three legacy port surfaces (LegacyTextTranslationService +
	// LegacyTranslatorService + LegacyMetadataTranslator). The
	// composition root constructs ONE OllamaTranslator per process
	// (godlike/06 SSOT for the translation logic); every consumer
	// field on ClipServices (Translation, Translator,
	// TranslationPort + any future metadata-translator dependency)
	// routes through this instance. Wrap the scriptGen (a
	// *ollama.Generator) — the canonical `TranslationCache` is
	// already wired into scriptGen by the SetTranslationCache call
	// above, so the OllamaTranslator's underlying gen.TranslateTextWithModel
	// call respects the same SQLite-backed cache lookup as the
	// legacy direct-call path. Per godlike/06 "one owner per fact",
	// the *ollama.Generator translation logic is owned by ONE
	// canonical Pyt-path (translation.ollama_translator.go) reachable
	// via all 4 ports. Tracking entry:
	// architecture/deprecations.yaml#TRANSLATION-LEGACY-SERVICES-MIGRATION
	ollamaTranslator := translation.NewOllamaTranslator(scriptGen, log)
	log.Info("Fase 9 step 2: OllamaTranslator wired (translation.TranslationPort + 3 legacy port surfaces)")

	// Commit H Phase 2 (June 2026): gemmamemory gemmamemory gate service + the
	// MemoryCacheAdapter wrapper are gone. The canonical engine no
	// longer consumes the gemmamemory cross-package type — the in-package
	// memoryCache interface (defined in cache_eviction_usecase.go) is
	// satisfied by nil here so the engine's `memoryGateChecker` type
	// assertion returns false at runtime and the cache path is skipped.
	// MemoryRepo (Repository struct, still in gemmamemory.go) is retained
	// because root.AI.MemoryRepo is consumed by startBackgroundJobs's
	// gemma-memory-sweeper (internal/app/lifecycle.go:393).
	engine := usecase.NewEngine(scriptGen, nil, log)

	return &AIBundle{
		OllamaClient:     ollamaClient,
		ScriptGen:        scriptGen,
		OllamaTranslator: ollamaTranslator,
		MemoryRepo:       adapters.NewRepository(dbs.main.DB),
		ScriptEngine:     engine,
	}, nil
}

// BuildMaintBundle constructs the periodic maintenance + deletion services.
func BuildMaintBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, drive *DriveBundle, repos *RepoBundle, search *SearchBundle, jobs *JobsBundle, outboxBundle *OutboxBundle) (*MaintBundle, error) {
	_ = ctx
	deletionSvc := deletion.NewDeletionService(
		repos.ClipsRepo, repos.ClipsRepo, repos.ClipsRepo,
		repos.VoiceoverRepo, repos.ImageRepo,
		drive.driveUploader, search.AssetTreeService, search.AssetIndexService,
		outboxBundle.Dispatcher,
		nil, // driveGoneChecker (Blocco 3.1 commit 3/3 — pre-commit-4/3 wiring forward-pointer)
		nil, // completionTxRunner (Blocco 3.1 commit 3/3 — pre-commit-4/3 wiring forward-pointer)
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
	uploader := drive.driveUploader
	if uploader == nil {
		uploader = &driveutil.Uploader{Log: log}
		log.Warn("BuildSyncBundle: drive uploader missing; using nil-service placeholder for disabled-drive bootstrap")
	}
	if outbox == nil || outbox.Dispatcher == nil {
		return nil, fmt.Errorf("BuildSyncBundle: outbox.Dispatcher is required — QDRANT-002 PR7 removed the legacy fallback; root.Outbox must be built first")
	}

	catalogSync, err := catalogsync.NewService(catalogsync.Deps{
		Uploader:   uploader,
		Targets:    syncTargets,
		AssetTree:  search.AssetTreeService,
		Dispatcher: outbox.Dispatcher,
		Log:        log,
	})
	if err != nil {
		return nil, fmt.Errorf("BuildSyncBundle: catalogsync.NewService: %w", err)
	}

	return &SyncBundle{
		CatalogSync: catalogSync,
	}, nil
}
