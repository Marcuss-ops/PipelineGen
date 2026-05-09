package bootstrap

import (
	"go.uber.org/zap"

	artlistHandler "velox/go-master/internal/api/handlers/artlist"
	artlistPkg "velox/go-master/internal/service/artlist"
	"velox/go-master/internal/module"
	"velox/go-master/pkg/config"
)

// ArtlistWiring holds the Artlist module wiring
type ArtlistWiring struct {
	Handler *artlistHandler.Handler
	Module  module.Module
	Service *artlistPkg.Service
}

// WireArtlist creates the Artlist service, handler, and module
func WireArtlist(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*ArtlistWiring, error) {
	// 1. Lifecycle
	artlistLifecycle := wireArtlistLifecycle(coreDeps, log)

	// 2. Catalog & Indexer
	clipCatalogRepo, clipIndexerSvc := wireArtlistCatalog(coreDeps, log)

	// 3. Resolvers
	assetDestResolver := wireAssetDestinationResolver(cfg, coreDeps, log)
	
	// Load presets early
	presetsConfig, err := artlistPkg.LoadPresets("config/artlist_presets.yaml")
	if err != nil {
		log.Warn("failed to load artlist presets, using defaults", zap.Error(err))
	}

	// 4. Service
	artlistSvc, err := wireArtlistService(cfg, coreDeps, artlistLifecycle, assetDestResolver, clipIndexerSvc, log)
	if err != nil {
		log.Warn("Failed to create Artlist service", zap.Error(err))
		return nil, err
	}

	// 5. Clip Resolver
	clipResolver := wireClipResolver(coreDeps, clipCatalogRepo, presetsConfig, log)
	if clipResolver != nil {
		coreDeps.ClipResolver = clipResolver
	}

	// 6. Handler
	handler := wireArtlistHandler(cfg, artlistSvc, coreDeps, clipResolver, presetsConfig, log)

	// 7. Module
	var mod module.Module
	if artlistSvc != nil && handler != nil {
		mod = module.NewArtlistModule(cfg, log, artlistSvc, handler)
		log.Info("created Artlist module")
	}

	return &ArtlistWiring{
		Handler: handler,
		Module:  mod,
		Service: artlistSvc,
	}, nil
}
