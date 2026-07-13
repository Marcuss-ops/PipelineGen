// Package app — catalog + youtube job handler late-bindings (extracted from
// composition.go NewComposition per PG-028 capability split, July 2026).
package app

import (
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// wireYoutubeCatalogJobBindings registers catalog sync + youtube clip extract
// handlers into jobs.Service. Extracted from NewComposition per PG-028.
func wireYoutubeCatalogJobBindings(sync *SyncBundle, domains *DomainBundle, jobs *JobsBundle) error {
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
	// PR-GEMMA-EXTRACT-IMPORTANT Step 7: register the LLM-driven extractor.
	if domains.ExtractImportantClipsJobHandler != nil && jobs.Service != nil {
		if err := domains.ExtractImportantClipsJobHandler.Register(jobs.Service); err != nil {
			return fmt.Errorf("%s: %w", job.TypeYouTubeClipExtractImportant, err)
		}
	}
	return nil
}

// appendYoutubeCatalogCriticalValidators populates the critical-handler
// validators slice with catalog sync + youtube clip extract bindings.
// Extracted from NewComposition per PG-028.
func appendYoutubeCatalogCriticalValidators(sync *SyncBundle, domains *DomainBundle, jobs *JobsBundle, validators *[]CriticalHandler) {
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
	// PR-GEMMA-EXTRACT-IMPORTANT Step 7: mirror critical-validator pattern.
	if domains.ExtractImportantClipsJobHandler != nil && jobs.Service != nil {
		h := domains.ExtractImportantClipsJobHandler
		*validators = append(*validators,
			CriticalHandler{
				Name: job.TypeYouTubeClipExtractImportant,
				Bind: func(svc *appjobs.Service) error {
					return h.Register(svc)
				},
			},
		)
	}
}
