package app

import (
	"fmt"

	"go.uber.org/zap"

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	artlistPkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func validateArtlistWiring(
	bundle *ArtlistBundle,
	dispatcher *outbox.Dispatcher,
	cfg *config.Config,
	log *zap.Logger,
) (finalization.AssetFinalizerTx, error) {
	if bundle == nil {
		return nil, ErrArtlistDepMissing{Kind: DepKindRunRepo, Field: "bundle"}
	}
	if bundle.Publisher == nil {
		return nil, ErrArtlistDepMissing{Kind: DepKindPublisher, Field: "bundle.Publisher"}
	}
	if dispatcher == nil {
		return nil, ErrArtlistDepMissing{Kind: DepKindDispatcher, Field: "dispatcher"}
	}
	if bundle.ClipsRepo == nil {
		return nil, ErrArtlistDepMissing{Kind: DepKindClipsRepo, Field: "bundle.ClipsRepo"}
	}
	if bundle.Jobs == nil || bundle.Jobs.Service == nil {
		return nil, ErrArtlistDepMissing{Kind: DepKindJobsService, Field: "bundle.Jobs.Service"}
	}
	if bundle.ClipIndexerService == nil {
		return nil, ErrArtlistDepMissing{Kind: DepKindIndexer, Field: "bundle.ClipIndexerService"}
	}

	finalizerTx := assetfinalizer.NewAssetTxFinalizer(log)
	if finalizerTx == nil {
		return nil, ErrArtlistDepMissing{Kind: DepKindFinalizer, Field: "finalizerTx"}
	}
	if err := validateArtlistScraperURL(cfg); err != nil {
		return nil, err
	}
	return finalizerTx, nil
}

func validateArtlistScraperURL(cfg *config.Config) error {
	if cfg == nil {
		return ErrArtlistDepMissing{
			Kind:   DepKindScraperURL,
			Field:  "cfg",
			Detail: "ART-002 P0.1 gate #5: cfg is nil; cannot evaluate scraper-URL fail-closed",
		}
	}
	if cfg.Features.ArtlistEnabled && cfg.External.ArtlistScraperServerURL == "" {
		return ErrArtlistDepMissing{
			Kind:   DepKindScraperURL,
			Field:  "cfg.External.ArtlistScraperServerURL",
			Detail: "ART-002 P0.1: cfg.Features.ArtlistEnabled=true but cfg.External.ArtlistScraperServerURL is empty — required env VELOX_ARTLIST_SCRAPER_SERVER_URL (without it the searcher chain silently degrades to per-call exec fallback). To disable Artlist set VELOX_FEATURE_ARTLIST_ENABLED=false",
		}
	}
	return nil
}

func WireArtlistJobBindings(artlistSvc *artlistPkg.Service, jobsBundle *JobsBundle) error {
	if artlistSvc == nil {
		return fmt.Errorf("WireArtlistJobBindings: artlistSvc is nil")
	}
	if jobsBundle == nil || jobsBundle.Service == nil {
		return fmt.Errorf("WireArtlistJobBindings: jobsBundle.Service is nil")
	}
	if err := artlistSvc.RegisterHandler(jobsBundle.Service); err != nil {
		return fmt.Errorf("%w: %w", ErrArtlistConsumerRegistrationFailed, err)
	}
	if !jobsBundle.Service.HasHandler(media.TypeArtlistRun) {
		return fmt.Errorf("%w: post-bind HasHandler(media.artlist) returned false (dispatcher silently dropped the Register call?)",
			ErrArtlistConsumerRegistrationFailed)
	}
	return nil
}
