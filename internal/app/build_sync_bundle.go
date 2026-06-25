package app

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
)

// BuildSyncBundle constructs ONLY the catalog→Drive sync. ProviderRegistry
// moved to BuildSearchBundle (PR4 review).
func BuildSyncBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, repos *RepoBundle, search *SearchBundle, process *ProcessBundle, drive *DriveBundle, outbox *OutboxBundle) (*SyncBundle, error) {
	_ = ctx
	_ = cfg
	_ = dbs
	_ = repos
	syncTargets := buildSyncTargets(cfg, repos.ClipsRepo, repos.ClipsRepo, repos.ClipsRepo)
	catalogSync := catalogsync.NewService(drive.DriveUploader, syncTargets,
		search.AssetIndexService, search.AssetTreeService, process.ClipIndexerService, log)
	if outbox != nil && outbox.Dispatcher != nil {
		catalogSync.SetDispatcher(outbox.Dispatcher)
	}

	return &SyncBundle{
		CatalogSync: catalogSync,
	}, nil
}
