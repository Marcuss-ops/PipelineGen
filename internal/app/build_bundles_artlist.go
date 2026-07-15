// Package app contains composition-root wiring for the Artlist capability.
package app

import (
	"context"

	"go.uber.org/zap"

	artlistPkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	clipindexer "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

var (
	_ artlistPkg.AssetStore = (*assets.ClipsRepository)(nil)
	_ artlistPkg.Indexer    = (*clipindexer.Service)(nil)
	_ artlistPkg.Dispatcher = (*outbox.Dispatcher)(nil)
	_ job.Service           = (*appjobs.Service)(nil)
)

// WireArtlist constructs the Artlist service, API module, and provider registry.
func WireArtlist(
	ctx context.Context,
	log *zap.Logger,
	cfg *config.Config,
	bundle *ArtlistBundle,
	dispatcher *outbox.Dispatcher,
	reader drivepkg.Reader,
	lifecycle drivepkg.FileLifecycle,
	metaWriter semantic.MetadataWriterPort,
	destResolver asset.Resolver,
) (*ArtlistWiring, error) {
	_ = ctx

	finalizerTx, err := validateArtlistWiring(bundle, dispatcher, cfg, log)
	if err != nil {
		return nil, err
	}
	_ = (job.Service)(bundle.Jobs.Service)

	log.Info("WireArtlist: ART-001 reversal wiring starting",
		zap.String("root_path", "/api/artlist/*"),
		zap.Bool("godlike_07_fail_closed", true),
	)

	runtime, err := buildArtlistRuntime(
		cfg,
		bundle,
		dispatcher,
		reader,
		lifecycle,
		metaWriter,
		finalizerTx,
		log,
	)
	if err != nil {
		return nil, err
	}

	service, err := buildArtlistService(cfg, log, bundle, dispatcher, destResolver, runtime)
	if err != nil {
		return nil, err
	}
	return finalizeArtlistWiring(cfg, log, bundle, service, runtime)
}
