package catalogsync

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/repository/clips"
)

// HandleJob processes a catalog.sync job.
func (s *Service) HandleJob(ctx context.Context, job *models.Job, tools *jobservice.JobTools) (map[string]any, error) {
	s.log.Info("handling catalog.sync job", zap.String("job_id", job.ID))

	var payload struct {
		Source string `json:"source"`
	}
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
		}
	}

	if tools.Progress != nil {
		tools.Progress(10, "Starting catalog synchronization")
	}

	var result map[string]any
	if payload.Source != "" {
		s.log.Info("syncing specific source", zap.String("source", payload.Source))
		summary, err := s.SyncSource(ctx, payload.Source)
		if err != nil {
			return nil, err
		}
		result = map[string]any{
			"ok":        true,
			"source":    payload.Source,
			"requested": summary.Requested,
			"synced":    summary.Synced,
			"failed":    summary.Failed,
		}
	} else {
		s.log.Info("syncing all sources")
		summary, err := s.SyncAll(ctx)
		if err != nil {
			return nil, err
		}
		result = map[string]any{
			"ok":     summary.OK,
			"synced": summary.Synced,
			"failed": summary.Failed,
			"roots":  summary.Roots,
		}
	}

	if tools.Progress != nil {
		tools.Progress(100, "Catalog synchronization completed")
	}

	return result, nil
}

// RegisterHandler registers this service as a handler for catalog.sync jobs.
func (s *Service) RegisterHandler(jobsSvc *jobservice.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(models.JobTypeCatalogSync, s.HandleJob)
		s.log.Info("registered catalog.sync job handler")
	}
}

// RegisterDriveFolderSyncHandler registers the handler for async drive.folder.sync jobs.
func (s *Service) RegisterDriveFolderSyncHandler(jobsSvc *jobservice.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(models.JobTypeDriveFolderSync, s.HandleDriveFolderSyncJob)
		s.log.Info("registered drive.folder.sync job handler")
	}
}

// HandleDriveFolderSyncJob processes an async drive.folder.sync job.
// It wraps SyncFolderID with progress reporting and mutex serialization.
func (s *Service) HandleDriveFolderSyncJob(ctx context.Context, job *models.Job, tools *jobservice.JobTools) (map[string]any, error) {
	var payload models.DriveFolderSyncPayload
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
		}
	}

	s.log.Info("handling drive.folder.sync job",
		zap.String("job_id", job.ID),
		zap.String("folder_id", payload.DriveFolderID),
		zap.String("source", payload.Source))

	if tools.Progress != nil {
		tools.Progress(5, "Starting Drive folder synchronization")
	}

	if payload.DriveFolderID == "" {
		return nil, fmt.Errorf("drive_folder_id is required")
	}

	source := payload.Source
	if source == "" {
		source = "drive"
	}
	mediaType := payload.MediaType
	if mediaType == "" {
		mediaType = "clip"
	}

	// Resolve the repository for the given source
	repo := s.resolveRepo(source)
	if repo == nil {
		return nil, fmt.Errorf("no repository configured for source: %s", source)
	}

	if tools.Progress != nil {
		tools.Progress(10, "Scanning Drive folder recursively")
	}

	summary, err := s.SyncFolderID(ctx, payload.DriveFolderID, source, payload.Name, mediaType, repo)
	if err != nil {
		return nil, err
	}

	if tools.Progress != nil {
		tools.Progress(100, "Drive folder synchronization completed")
	}

	return map[string]any{
		"ok":              true,
		"drive_folder_id": payload.DriveFolderID,
		"source":          source,
		"name":            summary.Name,
		"root_folder_id":  summary.RootFolderID,
		"requested":       summary.Requested,
		"synced":          summary.Synced,
		"failed":          summary.Failed,
	}, nil
}

// resolveRepo returns the clips.Repository for the given source name.
func (s *Service) resolveRepo(source string) *clips.Repository {
	for _, t := range s.targets {
		if strings.EqualFold(t.Source, source) {
			return t.Repo
		}
	}
	return nil
}
