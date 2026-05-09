package bootstrap

import (
	"go.uber.org/zap"
	"velox/go-master/internal/core/destination"
	"velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/assetdestination"
	"velox/go-master/internal/service/clipcatalog"
	"velox/go-master/internal/service/clipresolver"
	"velox/go-master/internal/service/matchingconfig"
	"velox/go-master/pkg/config"
)

func wireAssetDestinationResolver(cfg *config.Config, coreDeps *CoreDeps, log *zap.Logger) destination.Resolver {
	if coreDeps.DriveClient != nil {
		assetDest := assetdestination.NewResolver(cfg, log, coreDeps.DriveClient)
		return assetdestination.ToCoreResolver(assetDest)
	}
	return nil
}

func wireClipResolver(coreDeps *CoreDeps, clipCatalogRepo *clipcatalog.Repository, presetsConfig *artlist.PresetsConfig, log *zap.Logger) *clipresolver.Service {
	if clipCatalogRepo == nil {
		return nil
	}

	var harvestSvc clipresolver.ArtlistHarvestService
	if coreDeps.JobsService != nil {
		harvestSvc = clipresolver.NewJobHarvestService(coreDeps.JobsService, log, presetsConfig)
	}

	matchingCfg, err := matchingconfig.LoadMatchingConfig("config/matching.yaml")
	if err != nil {
		log.Warn("failed to load matching config, using defaults", zap.Error(err))
	}

	return clipresolver.NewService(clipCatalogRepo, harvestSvc, "config/ontology.yaml", matchingCfg)
}
