package app

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/mediaingest"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/artifacts"
	imgreg "github.com/Marcuss-ops/PipelineGen/internal/media/images"
	"github.com/Marcuss-ops/PipelineGen/internal/media/ingest"
	voingsvc "github.com/Marcuss-ops/PipelineGen/internal/media/voiceover"

	"go.uber.org/zap"
)

type MediaIngestWiring struct {
	Handler *mediaingest.MediaingestHandler
	Module  api.Module
	Service *ingest.Service
}

func WireMediaIngest(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (*MediaIngestWiring, error) {
	if coreDeps == nil || coreDeps.DriveClient == nil {
		return nil, nil
	}
	if coreDeps.ImageRepo == nil || coreDeps.VoiceoverRepo == nil || coreDeps.ClipsRepo == nil || coreDeps.AssetIndexService == nil {
		return nil, nil
	}

	imagesRegistry := imgreg.NewRegistryAdapter(coreDeps.ImageRepo, cfg.Storage.ImagesPath(), log)
	imagesLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    imagesRegistry,
		DriveClient: coreDeps.DriveClient,
		AssetIndex:  coreDeps.AssetIndexService,
		Store:       ingest.NewImageStoreAdapter(coreDeps.ImageRepo, cfg.Storage.ImagesPath()),
	}, log)

	voiceoverRegistry := voingsvc.NewVoiceoverRegistryAdapter(coreDeps.VoiceoverRepo)
	voiceoverLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    voiceoverRegistry,
		DriveClient: coreDeps.DriveClient,
		AssetIndex:  coreDeps.AssetIndexService,
		Store:       ingest.NewVoiceoverStoreAdapter(coreDeps.VoiceoverRepo),
	}, log)

	clipRegistry := artifacts.NewClipsRegistry(
		coreDeps.DB.DB,
		coreDeps.Assets.Repository(),
		coreDeps.Assets,
		coreDeps.Assets.LocationRepository(),
		coreDeps.Assets.ProcessingRepository(),
	)
	clipLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    clipRegistry,
		DriveClient: coreDeps.DriveClient,
		AssetIndex:  coreDeps.AssetIndexService,
		Store: ingest.NewClipStoreAdapter(
			coreDeps.DB.DB,
			coreDeps.Assets.Repository(),
			coreDeps.Assets,
			coreDeps.Assets.LocationRepository(),
			coreDeps.Assets.ProcessingRepository(),
		),
	}, log)

	stockRegistry := artifacts.NewClipsRegistry(
		coreDeps.DB.DB,
		coreDeps.Assets.Repository(),
		coreDeps.Assets,
		coreDeps.Assets.LocationRepository(),
		coreDeps.Assets.ProcessingRepository(),
	)
	stockLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    stockRegistry,
		DriveClient: coreDeps.DriveClient,
		AssetIndex:  coreDeps.AssetIndexService,
		Store: ingest.NewClipStoreAdapter(
			coreDeps.DB.DB,
			coreDeps.Assets.Repository(),
			coreDeps.Assets,
			coreDeps.Assets.LocationRepository(),
			coreDeps.Assets.ProcessingRepository(),
		),
	}, log)

	svc := ingest.NewService(cfg, log, coreDeps.DriveClient, map[ingest.Kind]*ingest.Pipeline{
		ingest.KindImage: {
			Kind:          ingest.KindImage,
			DefaultSource: "image",
			RootFolderID:  cfg.Drive.ImagesFolder(),
			RootFolder: func(req *ingest.Request) string {
				if isAIImageIngestSource(req) {
					if root := cfg.Drive.VideoAIFolder(); root != "" {
						return root
					}
				}
				return cfg.Drive.ImagesFolder()
			},
			Lifecycle: imagesLifecycle,
		},
		ingest.KindVoiceover: {
			Kind:          ingest.KindVoiceover,
			DefaultSource: "voiceover",
			RootFolderID:  cfg.Drive.VoiceoverFolder(),
			Lifecycle:     voiceoverLifecycle,
		},
		ingest.KindClip: {
			Kind:          ingest.KindClip,
			DefaultSource: "youtube",
			RootFolderID:  cfg.Drive.ClipsFolder(),
			Lifecycle:     clipLifecycle,
		},
		ingest.KindStock: {
			Kind:          ingest.KindStock,
			DefaultSource: "stock",
			RootFolderID:  cfg.Drive.StockFolder(),
			Lifecycle:     stockLifecycle,
		},
	})

	// Canonical constructor and type live in the mediaingest sub-package
	// (PR12 wrapper-removal pattern). The previous api.* type alias created
	// an import cycle (api → mediaingest → api) and was removed.
	handler := mediaingest.NewMediaingestHandler(svc)
	mod := mediaingest.NewMediaIngestModule(cfg, log, handler)

	return &MediaIngestWiring{
		Handler: handler,
		Module:  mod,
		Service: svc,
	}, nil
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
