// Package app wires the Assets HTTP module from immutable composition bundles.
package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"fmt"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

// registerAssets publishes the Assets module after every required repository,
// adapter, and application service has already been constructed by
// NewComposition. In particular, deletion and maintenance are owned by
// BuildMaintBundle; this phase never reconstructs or mutates either service.
func registerAssets(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *wiring.ComposeRoot, wiring *RegistryWiring) error {
	if root.Maint == nil || root.Maint.DeletionSvc == nil {
		return fmt.Errorf("wire registry: assets: canonical deletion service is not constructed")
	}

	assetsDeps := &AssetsModuleDeps{
		Core: CoreDeps{
			Repositories: RepositoryDeps{
				ClipsRepo:     root.Repos.ClipsRepo,
				VoiceoverRepo: root.Repos.VoiceoverRepo,
				ImageRepo:     root.Repos.ImageRepo,
			},
			Services: ServiceDeps{
				Assets:             root.Repos.Assets,
				AssetTreeService:   root.Search.AssetTreeService,
				AssetIndexService:  root.Search.AssetIndexService,
				MediaProcessor:     root.Process.MediaProcessor,
				CatalogSyncService: root.Sync.CatalogSync,
				ArtifactService:    root.Domains.ArtifactService,
			},
		},
		Search: SearchDeps{
			ClipIndexerService:    root.Process.ClipIndexerService,
			SearchFanOut:          wiring.searchFanOut,
			SearchBackendRegistry: wiring.searchBackends,
			SearchAggregator:      wiring.searchAgg,
		},
		Delivery: DeliveryDeps{
			Admin:     root.Drive.Admin,
			Publisher: root.Drive.Publisher,
		},
		Background: BackgroundDeps{
			IdempotencyStore:        root.Repos.IdempotencyStore,
			IdempotencyStoreHandler: wiring.idempotencyHandler,
		},
	}

	aw, err := WireAssets(
		cfg,
		log,
		assetsDeps,
		root.Repos.TextTrackRepo,
		root.Jobs,
		root.Drive.Lifecycle,
		root.Search.ProviderRegistry,
		root.Outbox.Dispatcher,
		root.Maint.DeletionSvc,
	)
	if err != nil {
		return fmt.Errorf("wire registry: assets build: %w", err)
	}
	if aw == nil {
		return fmt.Errorf("wire registry: assets build returned nil wiring")
	}

	wiring.Assets = aw
	if err := tryRegisterModuleStrict(registry, log, aw.Module, WithRegistrationPoint("register.Assets")); err != nil {
		return fmt.Errorf("wire registry: assets module: %w", err)
	}
	return nil
}
