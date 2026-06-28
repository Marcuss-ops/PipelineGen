// Package app — image-generation service bundle construction (Wave 15 PR4d-final).
//
// Extracted from compose_media.go per architecture/current.yaml::Wave 15
// pending #1. See build_bundles_voiceover.go for the rationale on
// build_bundles_<capability>.go as the canonical target and the
// private-helper (lowercase `build*`) naming convention.
package app

import (
	"context"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// buildImagesService creates the image generation service.
//
// PR4-H (June 2026): the 8 post-construction setters (SetNvidiaConfig,
// SetRemoteImageEndpointURL, SetVeloxBaseURL, SetGoogleAccountingConfig,
// SetMediaStore, SetLLMGenerator, SetVectorStore, SetMetadataWriter) were
// removed in Commit 3; their values are now passed as constructor args.
// The MetadataWriter is borrowed from BuildDomainBundle (voMetaWriter) to
// keep a single canonical instance shared with the voiceover service —
// Commit 1 introduced the dual-instance temporary state, Commit 3 collapses
// it via this single shared local in build_bundles_domain.go::BuildDomainBundle.
//
// Note: SetIngestService is NOT removed — it is the documented exception
// (called from registry.go::WireRegistry after MediaIngest is constructed).
func buildImagesService(
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
		imgservice.NewChromeImageProvider(cfg.Paths.PythonScriptsDir, 3, log), // FASE 8: 3 persistent profiles, per-profile queue
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
