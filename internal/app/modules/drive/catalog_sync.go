package drive

import (
	"fmt"

	assetswiring "github.com/Marcuss-ops/PipelineGen/internal/app/modules/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/catalogsync"
	platformdrive "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// BuildCatalogSyncService composes catalog-to-Drive reconciliation. PostgreSQL
// remains authoritative for media; the SQLite repository owns folder
// reconciliation until those writers migrate.
func BuildCatalogSyncService(deps CatalogSyncDeps) (*catalogsync.Service, error) {
	cfg, mediaDB, legacy := deps.Config, deps.MediaDB, deps.Legacy
	driveUploader, assetTree, dispatcher, log := deps.Uploader, deps.AssetTree, deps.Dispatcher, deps.Log
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
		// BGM is its own audio catalog. Without this target a drive.folder.sync
		// with source=music had no repository and forced the manual ffmpeg mix.
		{Name: "music", RootFolderID: cfg.Drive.MusicFolder(), Source: "music", MediaType: "audio", Repo: catalogRepo, Indexer: catalogIndexer},
	}
	reader := driveUploader
	if reader == nil {
		reader = &platformdrive.Uploader{Log: log}
		log.Warn("catalog sync: Drive reader missing; using nil-service placeholder for disabled-Drive bootstrap")
	}
	catalogReader := assetswiring.NewCatalogSyncSourceReader(reader, deps.MediaProber)
	service, err := catalogsync.NewService(catalogsync.Deps{
		Reader: catalogReader, Targets: targets, AssetTree: assetTree,
		Dispatcher: dispatcher, Log: log,
	})
	if err != nil {
		return nil, fmt.Errorf("catalog sync: construct service: %w", err)
	}
	return service, nil
}
