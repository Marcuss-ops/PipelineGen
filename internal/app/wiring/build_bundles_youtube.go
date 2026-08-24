// Package app — catalog + youtube job handler late-bindings (extracted from
// composition.go NewComposition per PG-028 capability split, July 2026).
package wiring

import (
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
)

// wireYoutubeCatalogJobBindings registers catalog sync + youtube clip extract
// handlers into jobs.Service. Extracted from NewComposition per PG-028.
func wireYoutubeCatalogJobBindings(sync *wiring.SyncBundle, domains *wiring.DomainBundle, jobs *wiring.JobsBundle) error {
	if sync.CatalogSync != nil && jobs.Service != nil {
		if err := sync.CatalogSync.RegisterHandler(jobs.Service); err != nil {
			return fmt.Errorf("catalogsync.catalog_sync: %w", err)
		}
		if err := sync.CatalogSync.RegisterDriveFolderSyncHandler(jobs.Service); err != nil {
			return fmt.Errorf("catalogsync.drive_folder_sync: %w", err)
		}
	}
	if domains.YoutubeClipService != nil && jobs.Service != nil {
		if err := domains.YoutubeClipService.RegisterHandler(jobs.Service); err != nil {
			return fmt.Errorf("youtube.clip_extract: %w", err)
		}
	}
	return nil
}

// appendYoutubeCatalogCriticalValidators populates the critical-handler
// validators slice with catalog sync + youtube clip extract bindings.
// Extracted from NewComposition per PG-028.
func appendYoutubeCatalogCriticalValidators(sync *wiring.SyncBundle, domains *wiring.DomainBundle, jobs *wiring.JobsBundle, validators *[]CriticalHandler) {
	if sync.CatalogSync != nil && jobs.Service != nil && jobs != nil {
		catSync := sync.CatalogSync
		*validators = append(*validators,
			CriticalHandler{
				Name: "catalogsync.catalog_sync",
				Bind: func(svc *appjobs.Service) error {
					return catSync.RegisterHandler(svc)
				},
			},
			CriticalHandler{
				Name: "catalogsync.drive_folder_sync",
				Bind: func(svc *appjobs.Service) error {
					return catSync.RegisterDriveFolderSyncHandler(svc)
				},
			},
		)
	}
	if domains.YoutubeClipService != nil && jobs.Service != nil {
		yt := domains.YoutubeClipService
		*validators = append(*validators,
			CriticalHandler{
				Name: "youtube.clip_extract",
				Bind: func(svc *appjobs.Service) error {
					return yt.RegisterHandler(svc)
				},
			},
		)
	}
}
