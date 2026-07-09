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

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/autotag"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// buildDomainAssetServices constructs the voiceover, books, ingest,
// images, lessons, and voiceover-sync services and populates the
// DomainBundle with them.
//
// godlike/06 SSOT: each service constructor is the SOLE canonical
// owner of its composition.
func buildDomainAssetServices(
	ctx context.Context,
	cfg *config.Config,
	dbs *databases,
	log *zap.Logger,
	drive *DriveBundle,
	repos *RepoBundle,
	search *SearchBundle,
	process *ProcessBundle,
	ai *AIBundle,
	outbox *OutboxBundle,
	mutationsDisp mutations.AssetMutationDispatcher,
	voMetaWriter *semantic.MetadataWriter,
	bundle *DomainBundle,
) error {
	if outbox == nil || outbox.Dispatcher == nil {
		return fmt.Errorf("compose domains: outbox.Dispatcher is required (PR-VO-A3 voiceover indexing handoff)")
	}
	voiceoverSvc, voiceoverRepo, voiceoverProcessItem, audioProcessor := buildVoiceoverService(ctx, cfg, dbs, log,
		drive.driveUploader,
		drive.Publisher,
		search.AssetIndexService, process.ClipIndexerService,
		drive.DestResolver,
		voMetaWriter, ai.OllamaTranslator,
		outbox.Dispatcher,
	)

	booksSvc, err := buildBooksService(cfg, dbs, log, voiceoverSvc, drive.Publisher, drive.driveUploader)
	if err != nil {
		return fmt.Errorf("compose domains: books transformer: %w", err)
	}

	ingestSvc := buildIngestService(cfg, log, dbs, drive.driveUploader, drive.Publisher, repos, search, mutationsDisp)

	imageSvc, metaWriter := buildImagesService(ctx, cfg, log,
		drive.driveUploader, repos.ClipsRepo, repos.ClipsRepo,
		drive.StyleRegistry, ai.ScriptGen,
		drive.MediaStore, drive.Publisher, repos.ImageRepo,
		voMetaWriter, ingestSvc,
		outbox.Dispatcher,
	)

	autotagSvc := autotag.NewService(dbs.main.DB, repos.Assets.Repository(), process.VLMClient, nil, log)

	// drive.DocClient (*drive.DocClientImpl) satisfies delivery.DocPublisher;
	// compile-time pin in delivery/doc_publisher.go locks conformance.
	docPublisher, ok := drive.DocClient.(delivery.DocPublisher)
	if !ok {
		return fmt.Errorf("compose domains: lessons: drive.DocClient does not satisfy delivery.DocPublisher (P1-6 migration incomplete)")
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

	bundle.VoiceoverService = voiceoverSvc
	bundle.VoiceoverSync = vosyncSvc
	bundle.VoiceoverProcessItem = voiceoverProcessItem
	bundle.ImageService = imageSvc
	bundle.IngestService = ingestSvc
	bundle.BooksService = booksSvc
	bundle.LessonsService = lessonsS
	bundle.MetaWriter = metaWriter
	bundle.AudioProcessor = audioProcessor

	// Realtime + Assoc stubs (package-removed or unwired).
	bundle.RealtimeMatcher = nil
	bundle.RealtimeSearch = nil
	bundle.AutotagService = autotagSvc
	bundle.AssocService = nil

	_ = assetsapi.RealtimeMatcher(nil)
	_ = usecase.RealtimeSearchService(nil)
	_ = usecase.AssocSearchService(nil)

	return nil
}
