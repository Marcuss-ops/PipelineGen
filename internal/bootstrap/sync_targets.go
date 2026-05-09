package bootstrap

import (
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/service/catalogsync"
	"velox/go-master/pkg/config"
)

// buildSyncTargets creates the catalog sync targets from configuration.
// This centralizes the sync target definitions in one place.
func buildSyncTargets(
	cfg *config.Config,
	clipsOnlyRepo *clips.Repository,
	clipsRepo *clips.Repository,
	artlistRepo *clips.Repository,
) []catalogsync.Target {
	targets := []catalogsync.Target{
		{
			Name:         "stock",
			RootFolderID: cfg.Drive.StockRootFolder,
			Source:       "stock",
			MediaType:    "stock",
			Repo:         clipsRepo,
		},
		{
			Name:         "clips",
			RootFolderID: cfg.Drive.ClipsRootFolder,
			Source:       "clips",
			MediaType:    "clip",
			Repo:         clipsOnlyRepo,
		},
		{
			Name:         "artlist",
			RootFolderID: cfg.Harvester.DriveFolderID,
			Source:       "artlist",
			MediaType:    "artlist",
			Repo:         artlistRepo,
		},
	}

	if cfg.Drive.ClipRootFolders != nil {
		for group, folderID := range cfg.Drive.ClipRootFolders {
			if folderID != "" {
				targets = append(targets, catalogsync.Target{
					Name:         "clips_" + group,
					RootFolderID: folderID,
					Source:       "clips",
					MediaType:    "clip",
					Repo:         clipsOnlyRepo,
				})
			}
		}
	}

	return targets
}
