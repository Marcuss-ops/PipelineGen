// Package app — catalog + youtube job handler late-bindings (extracted from
// composition.go NewComposition per PG-028 capability split, July 2026).
package wiring

import (
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	jobyoutube "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube"
	ytjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/instaeditpublishing"

	"go.uber.org/zap"
)

// wireYoutubeCatalogJobBindings registers catalog sync + youtube clip extract
// handlers into jobs.Service. Extracted from NewComposition per PG-028.
func wireYoutubeCatalogJobBindings(sync *SyncBundle, domains *DomainBundle, jobs *JobsBundle, log *zap.Logger) error {
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
	if jobs != nil && jobs.Service != nil {
		if err := wireYoutubeUploadBinding(jobs.Service, log); err != nil {
			return err
		}
	}
	return nil
}

// wireYoutubeUploadBinding registers the direct InstaEdit publishing handler
// when the workspace-scoped publishing credentials are present. The handler is
// intentionally optional: a deployment that only extracts clips must not fail
// to boot because it has no InstaEdit publishing key.
func wireYoutubeUploadBinding(svc *appjobs.Service, log *zap.Logger) error {
	if svc == nil {
		return nil
	}
	if log == nil {
		log = zap.NewNop()
	}
	cfg, err := instaeditpublishing.LoadConfig()
	if err != nil {
		log.Warn("youtube.upload handler disabled: InstaEdit publishing credentials are not configured", zap.Error(err))
		return nil
	}
	client, err := instaeditpublishing.NewClient(cfg)
	if err != nil {
		return fmt.Errorf("youtube.upload: %w", err)
	}
	handler := ytjobs.NewUploadJobHandler(client, log)
	if err := svc.RegisterHandler(jobyoutube.TypeUpload, appjobs.HandlerFunc(handler.HandleJob)); err != nil {
		return fmt.Errorf("youtube.upload: %w", err)
	}
	log.Info("registered youtube.upload job handler",
		zap.String("type", jobyoutube.TypeUpload),
		zap.Int64("workspace_id", cfg.WorkspaceID))
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
}
