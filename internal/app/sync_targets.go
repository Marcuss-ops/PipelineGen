package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/media/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/clips"
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
			RootFolderID: cfg.Drive.StockFolder(),
			Source:       "stock",
			MediaType:    "stock",
			Repo:         clipsRepo,
		},
		{
			Name:         "youtube",
			RootFolderID: cfg.Drive.ClipsFolder(),
			Source:       "youtube",
			MediaType:    "clip",
			Repo:         clipsOnlyRepo,
		},
		{
			Name:         "artlist",
			RootFolderID: cfg.Drive.ArtlistFolder(),
			Source:       "artlist",
			MediaType:    "artlist",
			Repo:         artlistRepo,
		},
	}

	// VideoAI: sync style subfolders so other components can resolve
	// style names to Drive folder IDs via AssetTree.
	if videoAIRoot := cfg.Drive.VideoAIFolder(); videoAIRoot != "" {
		targets = append(targets, catalogsync.Target{
			Name:         "videoai",
			RootFolderID: videoAIRoot,
			Source:       "videoai",
			MediaType:    "image",
			Repo:         artlistRepo, // reuse a repo for folder metadata only
		})
	}

	return targets
}
