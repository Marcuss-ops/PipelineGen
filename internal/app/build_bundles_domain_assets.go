package app

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	voiceoverreconcile "github.com/Marcuss-ops/PipelineGen/internal/application/assets/reconciliation/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/application/indexing"
	lessonsSvc "github.com/Marcuss-ops/PipelineGen/internal/application/lessons"
	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"

	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/enrichment"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/autotag"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	qdrantsearch "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/qdrantmm"
)

// buildDomainAssetServicesParams groups the dependencies required to
// construct the voiceover, books, ingest, images, lessons, and
// voiceover-sync services.
//
// PR-YAGNI-DOMAIN-ASSETS-WIRING (July 2026): replaces the 14 positional
// arguments of buildDomainAssetServices with a single struct.
type buildDomainAssetServicesParams struct {
	ctx           context.Context
	cfg           *config.Config
	dbs           *wiring.Databases
	log           *zap.Logger
	drive         *wiring.DriveBundle
	repos         *wiring.RepoBundle
	search        *wiring.SearchBundle
	process       *wiring.ProcessBundle
	ai            *wiring.AIBundle
	outbox        *wiring.OutboxBundle
	mutationsDisp mutations.AssetMutationDispatcher
	voMetaWriter  semantic.MetadataWriterPort
	bundle        *wiring.DomainBundle
	mediaConfig   mediaexec.ExecutionConfig
}

// buildDomainAssetServices constructs the voiceover, books, ingest,
// images, lessons, and voiceover-sync services and populates the
// wiring.DomainBundle with them.
//
// godlike/06 SSOT: each service constructor is the SOLE canonical
// owner of its composition.
func buildDomainAssetServices(params buildDomainAssetServicesParams) error {
	if params.outbox == nil || params.outbox.Dispatcher == nil {
		return fmt.Errorf("compose domains: outbox.Dispatcher is required (PR-VO-A3 voiceover indexing handoff)")
	}
	voiceoverDestResolver := params.drive.DestResolver
	// Voiceover group names are resolved against the canonical SQLite
	// asset tree rooted at Drive.VoiceoverFolder. Do not reuse the
	// generic wiring.DriveBundle resolver here: that resolver may belong to a
	// different asset family (for example images) and would turn a valid
	// voiceover group into an empty destination.
	if params.search != nil && params.cfg.Drive.VoiceoverFolder() != "" {
		resolved, resolveErr := newAssetTreeVoiceoverResolver(params.search.AssetTreeService, params.cfg.Drive.VoiceoverFolder(), params.log)
		if resolveErr != nil {
			return fmt.Errorf("compose domains: voiceover destination resolver: %w", resolveErr)
		}
		voiceoverDestResolver = resolved
	}
	canonicalCommitter := newCanonicalAssetCommitter(params.dbs.DualPool.Writer, params.outbox.EventsRepo, params.log)
	if params.repos != nil && params.repos.ClipsRepo != nil {
		wireCanonicalAssetStore(params.repos.ClipsRepo.AssetStoreSQLite, canonicalCommitter)
	}
	if params.repos != nil {
		wireCanonicalImageCommitter(params.repos.ImageRepo, canonicalCommitter)
	}
	voCommitter := canonicalCommitter
	voiceoverRepo, voiceoverProcessItem, audioProcessor, publishPool, err := buildVoiceoverPipeline(params.ctx, params.cfg, params.dbs, params.log,
		params.drive.DriveUploader,
		params.drive.Publisher,
		params.search.AssetIndexService, params.process.ClipIndexerService,
		voiceoverDestResolver,
		params.voMetaWriter,
		params.outbox.Dispatcher,
		voCommitter,
		params.mediaConfig,
	)
	if err != nil {
		return fmt.Errorf("compose domains: voiceover service: %w", err)
	}

	booksSvc, err := buildBooksService(params.cfg, params.dbs, params.log, params.drive.Publisher, params.drive.DriveUploader)
	if err != nil {
		return fmt.Errorf("compose domains: books transformer: %w", err)
	}

	ingestSvc := buildIngestService(params.cfg, params.log, params.dbs, params.drive.DriveUploader, params.drive.Publisher, params.repos, params.search, params.mutationsDisp, canonicalCommitter)

	imageSvc, metaWriter := buildImagesService(buildImagesParams{
		Cfg:           params.cfg,
		Log:           params.log,
		DriveUploader: params.drive.DriveUploader,
		StyleRegistry: params.drive.StyleRegistry,
		Publisher:     params.drive.Publisher,
		ImageRepo:     params.repos.ImageRepo,
		VOMetaWriter:  params.voMetaWriter,
		IngestSvc:     ingestSvc,
		Committer:     canonicalCommitter,
		Dispatcher:    params.outbox.Dispatcher,
	})

	var enrichState enrichment.EnrichStateMachinePort
	if params.repos.ClipsRepo != nil {
		esm, err := enrichment.NewEnrichStateMachine(params.repos.ClipsRepo)
		if err != nil {
			return fmt.Errorf("compose domains: enrich state machine: %w", err)
		}
		enrichState = esm
	}

	// Optional multi-frame video analysis ports. Each constructor is
	// best-effort: if any dependency is missing the autotag service
	// falls back to single-shot VLM analysis for video assets.
	var videoSampler indexing.PercentageFrameSampler
	proc := rustexec.NewConfiguredVideoProcessor(params.cfg.External.RustMusclesPath, params.cfg.External.FfmpegPath, params.mediaConfig.Policy, params.mediaConfig.Profile, params.log)
	if sampler, err := indexing.NewFFMPEGFrameSampler(proc); err == nil {
		videoSampler = sampler
	} else {
		params.log.Warn("compose domains: failed to build ffmpeg percentage sampler", zap.Error(err))
	}

	var visualVLM indexing.VLMClient
	if params.cfg.VLM.URL != "" {
		visualVLM = indexing.NewHTTPVLMClient(params.cfg.VLM.URL, 0)
	}

	var imageEmbedder qdrantsearch.ImageEmbedder
	if params.cfg.ClipIndexer.ServerURL != "" {
		imageEmbedder = qdrantsearch.NewImageEmbedderAdapter(
			qdrantsearch.ImageEmbedderConfig{ServerURL: params.cfg.ClipIndexer.ServerURL},
			nil,
			params.log,
		)
	}

	var frameIndexer mediamemory.KeyframeVisualIndexer
	if params.process.QdrantClient != nil {
		frameIndexer = qdrantmm.NewFrameQdrantIndexer(params.process.QdrantClient, params.log)
	}

	autotagSvc := autotag.NewService(autotag.ServiceDeps{
		DB: params.dbs.DualPool.Writer, Repo: params.repos.Assets.Repository(),
		VLMClient: params.process.VLMClient, Committer: canonicalCommitter,
		EnrichState: enrichState, Log: params.log,
		VideoAnalysis: autotag.VideoAnalysisDeps{
			Sampler: videoSampler, VLM: visualVLM,
			ImageEmbedder: imageEmbedder, FrameIndexer: frameIndexer,
		},
	})

	docPublisher := params.drive.DocPublisher
	lessonsS := lessonsSvc.NewService(
		&lessonsSvc.LessonsConfig{
			Enabled:             params.cfg.Lessons.Enabled,
			DefaultModel:        params.cfg.Lessons.DefaultModel,
			DefaultTone:         params.cfg.Lessons.DefaultTone,
			DefaultLanguage:     params.cfg.Lessons.DefaultLanguage,
			DefaultImageModel:   params.cfg.Lessons.DefaultImageModel,
			MaxParallelChapters: params.cfg.Lessons.MaxParallelChapters,
			OllamaURL:           params.cfg.External.OllamaURL,
		},
		params.ai.ScriptGen, imageSvc, docPublisher, params.log,
	)
	params.log.Info("Lessons service initialized", zap.Bool("enabled", params.cfg.Lessons.Enabled))

	var vosyncSvc *voiceoverreconcile.Service
	if voFolder := params.cfg.Drive.VoiceoverFolder(); voFolder != "" && voiceoverRepo != nil {
		vosyncSvc = voiceoverreconcile.NewService(params.drive.DriveUploader, voiceoverRepo, params.search.AssetTreeService, voFolder, params.log)
		params.log.Info("Voiceover sync service initialized", zap.String("root_folder_id", voFolder))
	}

	params.bundle.VoiceoverSync = vosyncSvc
	params.bundle.VoiceoverProcessItem = voiceoverProcessItem
	params.bundle.VoiceoverPublishPool = publishPool
	params.bundle.ImageService = imageSvc
	params.bundle.IngestService = ingestSvc
	params.bundle.BooksService = booksSvc
	params.bundle.LessonsService = lessonsS
	params.bundle.MetaWriter = metaWriter
	params.bundle.AudioProcessor = audioProcessor

	// Realtime + Assoc stubs (package-removed or unwired).
	params.bundle.RealtimeMatcher = nil
	params.bundle.RealtimeSearch = nil
	params.bundle.AutotagService = autotagSvc
	params.bundle.AssocService = nil

	_ = assetsapi.RealtimeMatcher(nil)
	_ = usecase.RealtimeSearchService(nil)
	_ = usecase.AssocSearchService(nil)

	return nil
}
