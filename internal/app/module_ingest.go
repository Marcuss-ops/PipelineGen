package app

import (
	"strings"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	imgapp "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	voapp "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/media/ingest"
	"go.uber.org/zap"
)

// MediaIngestWiring holds the MediaIngest module wiring.
type MediaIngestWiring struct {
	Handler *assets.MediaingestHandler
	Module  module.Module
	Service *ingest.Service
}

// WireMediaIngest creates the MediaIngest handler and module.
func WireMediaIngest(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (*MediaIngestWiring, error) {
	if coreDeps == nil || coreDeps.DriveClient == nil {
		return nil, nil
	}
	if coreDeps.ImageRepo == nil || coreDeps.VoiceoverRepo == nil || coreDeps.ClipsRepo == nil || coreDeps.AssetIndexService == nil {
		return nil, nil
	}
	imagesRegistry := imgapp.NewRegistryAdapter(coreDeps.ImageRepo, cfg.Storage.ImagesPath(), log)
	imagesLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: imagesRegistry, DriveClient: coreDeps.DriveClient, AssetIndex: coreDeps.AssetIndexService, Store: ingest.NewImageStoreAdapter(coreDeps.ImageRepo, cfg.Storage.ImagesPath())}, log)
	voiceoverRegistry := voapp.NewVoiceoverRegistryAdapter(coreDeps.VoiceoverRepo)
	voiceoverLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: voiceoverRegistry, DriveClient: coreDeps.DriveClient, AssetIndex: coreDeps.AssetIndexService, Store: ingest.NewVoiceoverStoreAdapter(coreDeps.VoiceoverRepo)}, log)
	clipRegistry := artifacts.NewClipsRegistry(coreDeps.DB.DB, coreDeps.Assets.Repository(), coreDeps.Assets, coreDeps.Assets.LocationRepository(), coreDeps.Assets.ProcessingRepository())
	clipLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: clipRegistry, DriveClient: coreDeps.DriveClient, AssetIndex: coreDeps.AssetIndexService, Store: ingest.NewClipStoreAdapter(coreDeps.DB.DB, coreDeps.Assets.Repository(), coreDeps.Assets, coreDeps.Assets.LocationRepository(), coreDeps.Assets.ProcessingRepository())}, log)
	stockRegistry := artifacts.NewClipsRegistry(coreDeps.DB.DB, coreDeps.Assets.Repository(), coreDeps.Assets, coreDeps.Assets.LocationRepository(), coreDeps.Assets.ProcessingRepository())
	stockLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: stockRegistry, DriveClient: coreDeps.DriveClient, AssetIndex: coreDeps.AssetIndexService, Store: ingest.NewClipStoreAdapter(coreDeps.DB.DB, coreDeps.Assets.Repository(), coreDeps.Assets, coreDeps.Assets.LocationRepository(), coreDeps.Assets.ProcessingRepository())}, log)
	svc := ingest.NewService(cfg, log, coreDeps.DriveClient, map[ingest.Kind]*ingest.Pipeline{
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
