// Package app — books/content service construction (PR4.3, June 2026).
//
// Extracted from dependencies.go so composition helpers are split per
// capability (AGENTS.md Pattern 5). Called from BuildDomainBundle in
// composition.go.

package app

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/application/books"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
)

// initBooksService creates the books processing service.
//
// PR4-H (June 2026): the SetDriveUploader setter was removed; driveUploader
// is now wired via the books.NewService constructor.
func initBooksService(cfg *config.Config, dbs *databases, log *zap.Logger, driveUploader *drive.Uploader, voiceoverSvc *voiceover.Service) *books.Service {
	booksSvc := books.NewService(
		&books.Config{
			Enabled:       cfg.Books.Enabled,
			ScriptPath:    cfg.Books.ScriptPath,
			PythonBin:     cfg.Books.PythonBin,
			DriveFolderID: cfg.Drive.BooksFolder(),
		},
		dbs.main.DB, cfg.Drive.BooksFolder(), log,
		voiceoverSvc, driveUploader,
	)
	log.Info("Books service initialized", zap.Bool("enabled", cfg.Books.Enabled))
	return booksSvc
}

// initImageService creates the image generation service.
//
// PR4-H (June 2026): the 8 post-construction setters (SetNvidiaConfig,
// SetRemoteImageEndpointURL, SetVeloxBaseURL, SetGoogleAccountingConfig,
// SetMediaStore, SetLLMGenerator, SetVectorStore, SetMetadataWriter) were
// removed in Commit 3; their values are now passed as constructor args.
// The MetadataWriter is borrowed from BuildDomainBundle (voMetaWriter) to
// keep a single canonical instance shared with the voiceover service —
// Commit 1 introduced the dual-instance temporary state, Commit 3 collapses
// it via this single shared local in composition.go::BuildDomainBundle.
//
// Note: SetIngestService is NOT removed — it is the documented exception
// (called from registry.go::WireRegistry after MediaIngest is constructed).
func initImageService(
	ctx context.Context, cfg *config.Config, log *zap.Logger,
	driveClient *gdrive.Service, clipsRepo *assets.ClipsRepository, artlistRepo *assets.ClipsRepository,
	styleRegistry *generation.StyleRegistry, scriptGen *ollama.Generator,
	mediaStore *drive.Store, imageRepo *assets.ImagesRepository,
	voMetaWriter *semantic.MetadataWriter,
	ingestSvc *ingest.Service,
	dispatcher *outbox.Dispatcher,
) (*imgservice.Service, *semantic.MetadataWriter) {
	// PG-034 (June 2026): vectorSvc arg removed — Qdrant capability deleted.

	imageService := imgservice.NewService(
		cfg,
		imageRepo, clipsRepo,
		driveClient,
		styleRegistry,
		imgservice.NvidiaConfig{APIKey: cfg.External.NvidiaAPIKey, Model: cfg.External.NvidiaModel},
		cfg.External.RemoteImageEndpointURL,
		cfg.External.VeloxBaseURL,
		imgservice.GoogleAccountingConfig{
			ServerURL:     cfg.GoogleAccounting.ServerURL,
			DownloadDir:   cfg.GoogleAccounting.DownloadDir,
			VidsProjectID: cfg.GoogleAccounting.VidsProjectID,
		},
		mediaStore,
		scriptGen,
		voMetaWriter,
		ingestSvc,
		dispatcher,
		log,
	)

	if cfg.External.RemoteImageEndpointURL != "" {
		log.Info("Remote image endpoint configured", zap.String("url", cfg.External.RemoteImageEndpointURL))
	}
	if cfg.External.VeloxBaseURL != "" {
		log.Info("Velox base URL for webhook push configured", zap.String("url", cfg.External.VeloxBaseURL))
	}

	_ = ctx // reserved for future customizer context flag

	// voMetaWriter is the canonical *semantic.MetadataWriter (single instance
	// shared with the voiceover service); returned here for continuity with
	// DomainBundle.MetaWriter.
	return imageService, voMetaWriter
}

// initVoiceoverService sets up the voiceover service and its repository.
//
// PR4-H (June 2026): SetSemanticTagger / SetTranslator / SetClipIndexer
// setters have been removed from voiceover.NewService — the three callbacks
// required for promo generation, translation, and post-enrichment indexing
// are now passed as constructor arguments (semanticTagger, translator,
// clipIndexer). This helper builds them from the canonical dependencies
// (metaWriter + scriptGen) declared in the composition root.
func initVoiceoverService(
	ctx context.Context,
	cfg *config.Config,
	dbs *databases,
	log *zap.Logger,
	driveClient *gdrive.Service,
	driveUploader *drive.Uploader,
	assetIndexService *assetindex.Service,
	clipIndexerService *clipindexer.Service,
	destResolver asset.Resolver,
	metaWriter *semantic.MetadataWriter,
	scriptGen *ollama.Generator,
) (*voiceover.Service, *assets.VoiceoversRepository) {

	voDir := cfg.Storage.VoiceoversPath()
	voRepo := assets.NewVoiceoversRepository(dbs.main.DB)

	// Voiceover registry adapter — wraps the SQLite vo repo as a
	// lifecycle.Registry so NewLifecycleFromDeps accepts it.
	voRegistryAdapter := voiceover.NewVoiceoverRegistryAdapter(voRepo)

	voLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    voRegistryAdapter,
		DriveClient: driveClient,
		AssetIndex:  assetIndexService,
	}, log)

	// Build semantic-tagger closure from metaWriter (used by promo
	// generation to enrich voiceover assets with search_text/tags).
	semanticTagger := func(ctx context.Context, prompt, style, mediaType, generator string) (*voiceover.SemanticTaggerResult, error) {
		if metaWriter == nil {
			return nil, fmt.Errorf("voiceover: metaWriter not wired (cannot enrich voiceover semantic metadata)")
		}
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
	}

	// Build translator closure from scriptGen (used by promo generation
	// to translate voiceover text into target language). Graceful
	// degradation: if scriptGen is nil, return input unchanged so promo
	// generation can still proceed.
	translator := func(ctx context.Context, text, targetLanguage string) (string, error) {
		if scriptGen == nil {
			return text, nil
		}
		return scriptGen.TranslateText(ctx, text, targetLanguage)
	}

	// Build clip-indexer closure (optional) — used by post-enrichment
	// to trigger embedding generation + Qdrant upsert for the voiceover
	// asset. Wire only when the indexer service is enabled.
	var clipIndexFn voiceover.ClipIndexFunc
	if clipIndexerService != nil && clipIndexerService.IsEnabled() {
		clipIndexFn = func(ctx context.Context, assetID string) error {
			return clipIndexerService.IndexClip(ctx, assetID)
		}
		log.Info("clip indexer wired into voiceover service for semantic search")
	}

	voService := voiceover.NewService(
		cfg, dbs.main.DB, cfg.Paths.PythonScriptsDir, voDir, log,
		driveUploader, voLifecycle, destResolver,
		semanticTagger, translator, clipIndexFn,
	)
	log.Info("Voiceover service initialized", zap.String("python_scripts_dir", cfg.Paths.PythonScriptsDir))

	return voService, voRepo
}
