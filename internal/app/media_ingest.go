package app

import (
	"velox/go-master/internal/api/handlers/mediaingest"
	"velox/go-master/internal/config"
	"velox/go-master/internal/media/assetregistry"
	imgreg "velox/go-master/internal/media/images"
	"velox/go-master/internal/media/ingest"
	voingsvc "velox/go-master/internal/media/voiceover"
	"velox/go-master/internal/module"

	"go.uber.org/zap"
)

type MediaIngestWiring struct {
	Handler *mediaingest.Handler
	Module  module.Module
	Service *ingest.Service
}

func WireMediaIngest(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (*MediaIngestWiring, error) {
	if coreDeps == nil || coreDeps.DriveClient == nil {
		return nil, nil
	}
	if coreDeps.ImageRepo == nil || coreDeps.VoiceoverRepo == nil || coreDeps.ClipsOnlyRepo == nil || coreDeps.StockDriveRepo == nil || coreDeps.AssetIndexService == nil {
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

	clipRegistry := assetregistry.NewClipsRegistry(coreDeps.ClipsOnlyRepo)
	clipLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    clipRegistry,
		DriveClient: coreDeps.DriveClient,
		AssetIndex:  coreDeps.AssetIndexService,
		Store:       ingest.NewClipStoreAdapter(coreDeps.ClipsOnlyRepo),
	}, log)

	stockRegistry := assetregistry.NewClipsRegistry(coreDeps.StockDriveRepo)
	stockLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    stockRegistry,
		DriveClient: coreDeps.DriveClient,
		AssetIndex:  coreDeps.AssetIndexService,
		Store:       ingest.NewClipStoreAdapter(coreDeps.StockDriveRepo),
	}, log)

	svc := ingest.NewService(cfg, log, coreDeps.DriveClient, map[ingest.Kind]*ingest.Pipeline{
		ingest.KindImage: {
			Kind:          ingest.KindImage,
			DefaultSource: "image",
			RootFolderID:  cfg.Drive.ImagesRootFolder,
			Lifecycle:     imagesLifecycle,
		},
		ingest.KindVoiceover: {
			Kind:          ingest.KindVoiceover,
			DefaultSource: "voiceover",
			RootFolderID:  cfg.Drive.VoiceoverRootFolder,
			Lifecycle:     voiceoverLifecycle,
		},
		ingest.KindClip: {
			Kind:          ingest.KindClip,
			DefaultSource: "youtube",
			RootFolderID:  cfg.Drive.ClipsRootFolder,
			Lifecycle:     clipLifecycle,
		},
		ingest.KindStock: {
			Kind:          ingest.KindStock,
			DefaultSource: "stock",
			RootFolderID:  cfg.Drive.StockRootFolder,
			Lifecycle:     stockLifecycle,
		},
	})

	handler := mediaingest.NewHandler(svc)
	mod := module.NewMediaIngestModule(cfg, log, handler)

	return &MediaIngestWiring{
		Handler: handler,
		Module:  mod,
		Service: svc,
	}, nil
}
