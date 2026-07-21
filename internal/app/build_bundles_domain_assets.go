package app

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	voiceoverreconcile "github.com/Marcuss-ops/PipelineGen/internal/application/assets/reconciliation/voiceover"
	lessonsSvc "github.com/Marcuss-ops/PipelineGen/internal/application/lessons"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"

	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/enrichment"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/autotag"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
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
	dbs           *databases
	log           *zap.Logger
	drive         *DriveBundle
	repos         *RepoBundle
	search        *SearchBundle
	process       *ProcessBundle
	ai            *AIBundle
	outbox        *OutboxBundle
	mutationsDisp mutations.AssetMutationDispatcher
	voMetaWriter  semantic.MetadataWriterPort
	bundle        *DomainBundle
}

// buildDomainAssetServices constructs the voiceover, books, ingest,
// images, lessons, and voiceover-sync services and populates the
// DomainBundle with them.
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
	// generic DriveBundle resolver here: that resolver may belong to a
	// different asset family (for example images) and would turn a valid
	// voiceover group into an empty destination.
	if params.search != nil && params.cfg.Drive.VoiceoverFolder() != "" {
		resolved, resolveErr := newAssetTreeVoiceoverResolver(params.search.AssetTreeService, params.cfg.Drive.VoiceoverFolder(), params.log)
		if resolveErr != nil {
			return fmt.Errorf("compose domains: voiceover destination resolver: %w", resolveErr)
		}
		voiceoverDestResolver = resolved
	}
	voiceoverSvc, voiceoverRepo, voiceoverProcessItem, audioProcessor := buildVoiceoverService(params.ctx, params.cfg, params.dbs, params.log,
		params.drive.driveUploader,
		params.drive.Publisher,
		params.search.AssetIndexService, params.process.ClipIndexerService,
		voiceoverDestResolver,
		params.voMetaWriter, params.ai.OllamaTranslator,
		params.outbox.Dispatcher,
	)

	booksSvc, err := buildBooksService(params.cfg, params.dbs, params.log, params.drive.Publisher, params.drive.driveUploader)
	if err != nil {
		return fmt.Errorf("compose domains: books transformer: %w", err)
	}

	ingestSvc := buildIngestService(params.cfg, params.log, params.dbs, params.drive.driveUploader, params.drive.Publisher, params.repos, params.search, params.mutationsDisp)

	imageSvc, metaWriter := buildImagesService(buildImagesParams{
		Cfg:           params.cfg,
		Log:           params.log,
		DriveUploader: params.drive.driveUploader,
		StyleRegistry: params.drive.StyleRegistry,
		ScriptGen:     params.ai.ScriptGen,
		Publisher:     params.drive.Publisher,
		ImageRepo:     params.repos.ImageRepo,
		VOMetaWriter:  params.voMetaWriter,
		IngestSvc:     ingestSvc,
		Committer:     sqassets.NewSQLiteAssetCommitter(params.dbs.dualPool.Writer, params.outbox.EventsRepo, params.log),
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
	autotagSvc := autotag.NewService(params.dbs.dualPool.Writer, params.repos.Assets.Repository(), params.process.VLMClient, params.mutationsDisp, enrichState, params.log)

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
		vosyncSvc = voiceoverreconcile.NewService(params.drive.driveUploader, voiceoverRepo, params.search.AssetTreeService, voFolder, params.log)
		params.log.Info("Voiceover sync service initialized", zap.String("root_folder_id", voFolder))
	}

	params.bundle.VoiceoverService = voiceoverSvc
	params.bundle.VoiceoverSync = vosyncSvc
	params.bundle.VoiceoverProcessItem = voiceoverProcessItem
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
