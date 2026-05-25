package app

import (
	"velox/go-master/internal/config"
	"velox/go-master/internal/media/catalogsync"
	"velox/go-master/internal/repository/clips"
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
			Name:         "youtube",
			RootFolderID: cfg.Drive.ClipsRootFolder,
			Source:       "youtube",
			MediaType:    "clip",
			Repo:         clipsOnlyRepo,
		},
		{
			Name:         "artlist",
			RootFolderID: cfg.Drive.RootFolder(),
			Source:       "artlist",
			MediaType:    "artlist",
			Repo:         artlistRepo,
		},
	}

	if cfg.Drive.ClipRootFolders != nil {
		for group, folderID := range cfg.Drive.ClipRootFolders {
			if folderID != "" {
				targets = append(targets, catalogsync.Target{
					Name:         "youtube_" + group,
					RootFolderID: folderID,
					Source:       "youtube",
					MediaType:    "clip",
					Repo:         clipsOnlyRepo,
				})
			}
		}
	}

	return targets
}
