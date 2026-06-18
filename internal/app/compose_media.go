package app

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/config"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/media/books"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/media/images"
	"github.com/Marcuss-ops/PipelineGen/internal/media/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/media/videomuscles"
	"github.com/Marcuss-ops/PipelineGen/internal/media/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/images"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/monitors"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/voiceovers"
	"github.com/Marcuss-ops/PipelineGen/internal/sources/youtube"
	pkgffmpeg "github.com/Marcuss-ops/PipelineGen/pkg/media/ffmpeg"
)

// MediaDomain holds media-specific services produced by composeMediaDomain.
type MediaDomain struct {
	YoutubeClipService *youtube.Service
	VoiceoverService   *voiceover.Service
	VoiceoverRepo      *voiceovers.Repository
	BooksService       *books.Service
	ClipsRepo          *clips.Repository // single shared repository (replaces ClipsRepo + ArtlistRepo)
	ScriptsRepo        *scripts.ScriptRepository
	ImageRepo          *images.Repository
	ImageService       *imgservice.Service
	MetaWriter         *semantic.MetadataWriter
	MonitorsRepo       *monitors.Repository
}

// composeMediaDomain initializes all media domain services.
// These depend on core infrastructure and domain-specific configuration.
func composeMediaDomain(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, core *CoreInfra) (*MediaDomain, error) {
	// Single shared clips repository — core.ClipsOnlyRepo is the canonical instance.
	// compose_media previously created separate clipsRepo and artlistRepo instances
	// on the same DB; PR 7 unified them into one shared pointer.
	clipsRepo := core.ClipsOnlyRepo
	scriptsRepo := scripts.NewScriptRepository(dbs.main.DB)
	imageRepo := images.NewRepository(dbs.main.DB)

	// YouTube Lifecycle & Video Pipeline
	clipsRegistry := assetregistry.NewClipsRegistry(
		dbs.main.DB,
		core.AssetRepo,
		core.AssetQueryService,
		core.AssetLocationRepo,
		core.AssetProcessingRepo,
	)
	ytLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    clipsRegistry,
		DriveClient: core.DriveClient,
		AssetIndex:  core.AssetIndexService,
	}, log)

	clipProcessor := pkgffmpeg.New(cfg)
	videoPipeline := videomuscles.NewPipeline(cfg, log, clipProcessor)

	// YouTube Clip Service
	monitorsRepo := monitors.NewRepository(dbs.main.DB)
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
