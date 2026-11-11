package drive

import (
	"database/sql"
	"fmt"

	assetswiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	platformdrive "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	legacyassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
	"go.uber.org/zap"
)

// BuildCatalogSyncService composes catalog-to-Drive reconciliation. PostgreSQL
// remains authoritative for media; the SQLite repository owns folder
// reconciliation until those writers migrate.
func BuildCatalogSyncService(
	cfg *config.Config,
	mediaDB *sql.DB,
	legacy *legacyassets.ClipsRepository,
	driveUploader *platformdrive.Uploader,
	assetTree *assettree.Service,
	dispatcher *outbox.Dispatcher,
	log *zap.Logger,
) (*catalogsync.Service, error) {
	if cfg == nil {
		return nil, fmt.Errorf("catalog sync: config is required")
	}
	if dispatcher == nil {
		return nil, fmt.Errorf("catalog sync: outbox dispatcher is required")
	}
	catalogRepo, catalogIndexer := assetswiring.NewCatalogSyncRepository(mediaDB, legacy)
	targets := []catalogsync.Target{
		{Name: "stock", RootFolderID: cfg.Drive.StockFolder(), Source: "stock", MediaType: "video", Repo: catalogRepo, Indexer: catalogIndexer},
		{Name: "youtube", RootFolderID: cfg.Drive.ClipsFolder(), Source: "youtube", MediaType: "video", Repo: catalogRepo, Indexer: catalogIndexer},
		// Artlist assets are reconciled from the stock root after migration.
		{Name: "sound_effects", RootFolderID: cfg.Drive.SoundEffectsFolder(), Source: "sound_effect", MediaType: "audio", Repo: catalogRepo, Indexer: catalogIndexer},
	}
	reader := driveUploader
	if reader == nil {
		reader = &platformdrive.Uploader{Log: log}
		log.Warn("catalog sync: Drive reader missing; using nil-service placeholder for disabled-Drive bootstrap")
	}
	catalogReader := assetswiring.NewCatalogSyncSourceReader(reader)
	service, err := catalogsync.NewService(catalogsync.Deps{
		Reader: catalogReader, Targets: targets, AssetTree: assetTree,
		Dispatcher: dispatcher, Log: log,
	})
	if err != nil {
		return nil, fmt.Errorf("catalog sync: construct service: %w", err)
	}
	return service, nil
}
