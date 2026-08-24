package adapters

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/sourcing"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	assetsrepo "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	clipindexer "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// NewYoutubeIndexDispatcherAdapter wires the outbox dispatcher + asset tree.
func NewYoutubeIndexDispatcherAdapter(disp *outbox.Dispatcher, tree *assettree.Service) *YoutubeIndexDispatcherAdapter {
	return &YoutubeIndexDispatcherAdapter{disp: disp, tree: tree}
}

// NewYoutubeEnrichmentAdapter wires the four enrichment sub-ports.
func NewYoutubeEnrichmentAdapter(jobs sourcing.JobsPort, enrichment sourcing.EnrichmentPort, search sourcing.SearchProviderPort, config sourcing.ConfigPort) *YoutubeEnrichmentAdapter {
	return &YoutubeEnrichmentAdapter{jobs: jobs, enrichment: enrichment, search: search, config: config}
}

// NewSourcingEnrichmentAdapter wires the canonical ClipEnricher port.
func NewSourcingEnrichmentAdapter(enricher appclips.ClipEnricher) *SourcingEnrichmentAdapter {
	return &SourcingEnrichmentAdapter{enricher: enricher}
}

// NewSourcingConfigAdapter wires the sourcing config port.
func NewSourcingConfigAdapter(cfg *config.Config) *SourcingConfigAdapter {
	return &SourcingConfigAdapter{cfg: cfg}
}

// NewSourcingSearchAdapter wires the provider registry for search.
func NewSourcingSearchAdapter(registry *providers.Registry) *SourcingSearchAdapter {
	return &SourcingSearchAdapter{registry: registry}
}

// NewSourcingFetchAdapter wires the provider registry for fetch.
func NewSourcingFetchAdapter(registry *providers.Registry) *SourcingFetchAdapter {
	return &SourcingFetchAdapter{registry: registry}
}

// NewSourcingClipStoreAdapter wires the clips repository for sourcing lookups.
func NewSourcingClipStoreAdapter(repo *assetsrepo.ClipsRepository) *SourcingClipStoreAdapter {
	return &SourcingClipStoreAdapter{repo: repo}
}

// NewSourcingPublisherAdapter wires the delivery publisher.
func NewSourcingPublisherAdapter(publisher delivery.Publisher) *SourcingPublisherAdapter {
	return &SourcingPublisherAdapter{publisher: publisher}
}

// NewSourcingTranscriberAdapter wires the transcriber config + logger.
func NewSourcingTranscriberAdapter(cfg *config.Config, log *zap.Logger) *SourcingTranscriberAdapter {
	return &SourcingTranscriberAdapter{cfg: cfg, log: log}
}

// NewSourcingMetadataAdapter wires the metadata sidecar adapter.
func NewSourcingMetadataAdapter(cfg *config.Config, admin driveutil.Admin, reader driveutil.Reader, lifecycle driveutil.FileLifecycle, publisher delivery.Publisher, log *zap.Logger) *SourcingMetadataAdapter {
	return &SourcingMetadataAdapter{cfg: cfg, admin: admin, reader: reader, lifecycle: lifecycle, publisher: publisher, log: log}
}

// NewZapSourcingLogger wires the structured logger.
func NewZapSourcingLogger(log *zap.Logger) *ZapSourcingLogger {
	return &ZapSourcingLogger{log: log}
}

// NewClipIndexerAdapter wires the clip indexer service.
func NewClipIndexerAdapter(inner *clipindexer.Service) *ClipIndexerAdapter {
	return &ClipIndexerAdapter{inner: inner}
}
