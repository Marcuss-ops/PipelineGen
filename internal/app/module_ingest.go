package app

import (
	"database/sql"
	"strings"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	imgapp "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	voapp "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/application/ingest"
	gdrive "google.golang.org/api/drive/v3"
	"go.uber.org/zap"
)

// MediaIngestBundle is the capability bundle for the media-ingest module.
//
// PR4d-chunk2 (June 2026): wraps the 11 cross-bundle reads of WireMediaIngest
// into 7 typed fields.
type MediaIngestBundle struct {
	DB                *sql.DB
	Assets            *asset.Service
	DriveClient       *gdrive.Service
	ImageRepo         *sqassets.ImagesRepository
	VoiceoverRepo     *sqassets.VoiceoversRepository
	ClipsRepo         *sqassets.ClipsRepository
	AssetIndexService *assetindex.Service
}

// MediaIngestWiring holds the MediaIngest module wiring.
type MediaIngestWiring struct {
	Handler *assets.MediaingestHandler
	Module  module.Module
	Service *ingest.Service
}

// WireMediaIngest creates the MediaIngest handler and module.
//
// PR4d-chunk2 (June 2026): takes *MediaIngestBundle.
func WireMediaIngest(cfg *config.Config, log *zap.Logger, bundle *MediaIngestBundle) (*MediaIngestWiring, error) {
	if bundle == nil || bundle.DriveClient == nil {
		return nil, nil
	}
	if bundle.ImageRepo == nil || bundle.VoiceoverRepo == nil || bundle.ClipsRepo == nil || bundle.AssetIndexService == nil {
		return nil, nil
	}
	imagesRegistry := imgapp.NewRegistryAdapter(bundle.ImageRepo, cfg.Storage.ImagesPath(), log)
	imagesLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: imagesRegistry, DriveClient: bundle.DriveClient, AssetIndex: bundle.AssetIndexService, Store: ingest.NewImageStoreAdapter(bundle.ImageRepo, cfg.Storage.ImagesPath())}, log)
	voiceoverRegistry := voapp.NewVoiceoverRegistryAdapter(bundle.VoiceoverRepo)
	voiceoverLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: voiceoverRegistry, DriveClient: bundle.DriveClient, AssetIndex: bundle.AssetIndexService, Store: ingest.NewVoiceoverStoreAdapter(bundle.VoiceoverRepo)}, log)
	clipRegistry := artifacts.NewClipsRegistry(bundle.DB, bundle.Assets.Repository(), bundle.Assets, bundle.Assets.LocationRepository(), bundle.Assets.ProcessingRepository())
	clipLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: clipRegistry, DriveClient: bundle.DriveClient, AssetIndex: bundle.AssetIndexService, Store: ingest.NewClipStoreAdapter(bundle.DB, bundle.Assets.Repository(), bundle.Assets, bundle.Assets.LocationRepository(), bundle.Assets.ProcessingRepository())}, log)
	stockRegistry := artifacts.NewClipsRegistry(bundle.DB, bundle.Assets.Repository(), bundle.Assets, bundle.Assets.LocationRepository(), bundle.Assets.ProcessingRepository())
	stockLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: stockRegistry, DriveClient: bundle.DriveClient, AssetIndex: bundle.AssetIndexService, Store: ingest.NewClipStoreAdapter(bundle.DB, bundle.Assets.Repository(), bundle.Assets, bundle.Assets.LocationRepository(), bundle.Assets.ProcessingRepository())}, log)
	svc := ingest.NewService(cfg, log, bundle.DriveClient, map[ingest.Kind]*ingest.Pipeline{
		ingest.KindImage:     {Kind: ingest.KindImage, DefaultSource: "image", RootFolderID: cfg.Drive.ImagesFolder(), Lifecycle: imagesLifecycle},
		ingest.KindVoiceover: {Kind: ingest.KindVoiceover, DefaultSource: "voiceover", RootFolderID: cfg.Drive.VoiceoverFolder(), Lifecycle: voiceoverLifecycle},
		ingest.KindClip:      {Kind: ingest.KindClip, DefaultSource: "youtube", RootFolderID: cfg.Drive.ClipsFolder(), Lifecycle: clipLifecycle},
		ingest.KindStock:     {Kind: ingest.KindStock, DefaultSource: "stock", RootFolderID: cfg.Drive.StockFolder(), Lifecycle: stockLifecycle},
	})
	handler := assets.NewMediaingestHandler(svc)
	mod := assets.NewMediaIngestModule(cfg, log, handler)
	return &MediaIngestWiring{Handler: handler, Module: mod, Service: svc}, nil
}

func isAIImageIngestSource(req *ingest.Request) bool {
	if req == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(req.Source)) {
	case "google-vids", "google-vids-image", "google-slides", "google-flow", "nvidia", "nvidia-local", "local-nim", "flux-1-dev", "flux-1-schnell", "flux.1-schnell", "flux1-schnell", "flux-2-klein", "flux.2-klein-4b", "flux-2-klein-4b":
		return true
	default:
		return false
	}
}
