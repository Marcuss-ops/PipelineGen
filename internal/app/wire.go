package app

import (
	"velox/go-master/internal/module"
	"velox/go-master/internal/config"

	"go.uber.org/zap"
)

// AppDeps holds the minimal initialized dependencies for the server.
type AppDeps struct {
	Registry *module.Registry
	Cleanup  func()
}

// WireServices initializes the full server composition root.
func WireServices(cfg *config.Config, log *zap.Logger, mode string) (*AppDeps, error) {
	coreDeps, coreClean, err := initCoreMinimal(cfg, log, mode)
	if err != nil {
		return nil, err
	}

	// Wire up the registry with all modules
	registryWiring, err := WireRegistry(cfg, log, coreDeps)
	if err != nil {
		return nil, err
	}

	cleanup := func() {
		if registryWiring.ArtlistSvc != nil && registryWiring.ArtlistSvc.Service != nil {
			registryWiring.ArtlistSvc.Service.Close()
		}
		if coreClean != nil {
			coreClean()
		}
	}

	return &AppDeps{
		Registry: registryWiring.Registry,
		Cleanup:  cleanup,
	}, nil
}

// WireMinimal creates a minimal server with core services only.
// This is the recommended entry point for local tools and minimal deployments.
func WireMinimal(cfg *config.Config, log *zap.Logger, mode string) (*AppDeps, error) {
	_, coreClean, err := initCoreMinimal(cfg, log, mode)
	if err != nil {
		return nil, err
	}
	return &AppDeps{
		Registry: nil,
		Cleanup:  coreClean,
	}, nil
}
