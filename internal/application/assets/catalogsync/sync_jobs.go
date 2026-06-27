package catalogsync

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

// HandleJob processes a catalog.sync job.
func (s *Service) HandleJob(ctx context.Context, job *appjobs.Job, tools *appjobs.JobTools) (map[string]any, error) {
	s.log.Info("handling catalog.sync job", zap.String("job_id", job.ID))

	// PR 4 (June 2026 — codex/clips-reconcile-real) extended the
	// catalog.sync payload with FolderID + Fix + DryRun so the same
	// job type covers three sync granularities:
	//   - FolderID != ""  → sync one specific Drive folder under Source.
	//   - Source != ""    → sync all folders under one source.
	//   - both empty      → sync all configured sources.
	// Fix and DryRun are propagated into the result map as hints but
	// the underlying SyncFolderID/SyncSource/SyncAll dispatchers do
	// not yet honour them — that's a follow-up ticket.
	var payload struct {
		Source   string `json:"source"`
		FolderID string `json:"folder_id,omitempty"`
		Fix      bool   `json:"fix,omitempty"`
		DryRun   bool   `json:"dry_run,omitempty"`
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
	switch {
	case payload.FolderID != "":
		s.log.Info("syncing specific folder",
			zap.String("folder_id", payload.FolderID),
			zap.String("source", payload.Source),
			zap.Bool("fix", payload.Fix),
			zap.Bool("dry_run", payload.DryRun))
		// resolveRepo falls back to defaultClipsRepo ("drive") for
		// ad-hoc source names — see resolveRepo docstring for the
		// source-lookup precedence rules.
		repo := s.resolveRepo(payload.Source)
		if repo == nil {
			return nil, fmt.Errorf("no repository configured for source=%q (folder_id=%q)",
				payload.Source, payload.FolderID)
		}
		summary, err := s.SyncFolderID(ctx, payload.FolderID, payload.Source, "", "clip", repo)
		if err != nil {
			return nil, err
		}
		result = map[string]any{
			"ok":              true,
			"drive_folder_id": payload.FolderID,
			"source":          payload.Source,
			"name":            summary.Name,
			"root_folder_id":  summary.RootFolderID,
			"requested":       summary.Requested,
			"synced":          summary.Synced,
			"failed":          summary.Failed,
			"fix":             payload.Fix,
			"dry_run":         payload.DryRun,
		}
	case payload.Source != "":
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
			"fix":       payload.Fix,
			"dry_run":   payload.DryRun,
		}
	default:
		s.log.Info("syncing all sources")
		summary, err := s.SyncAll(ctx)
		if err != nil {
			return nil, err
		}
		result = map[string]any{
			"ok":      summary.OK,
			"synced":  summary.Synced,
			"failed":  summary.Failed,
			"roots":   summary.Roots,
			"fix":     payload.Fix,
			"dry_run": payload.DryRun,
		}
	}

	if tools.Progress != nil {
		tools.Progress(100, "Catalog synchronization completed")
	}

	return result, nil
}

// RegisterHandler registers this service as a handler for catalog.sync jobs.
func (s *Service) RegisterHandler(jobsSvc *appjobs.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(appjobs.TypeCatalogSync, s.HandleJob)
		s.log.Info("registered catalog.sync job handler")
	}
}

// RegisterDriveFolderSyncHandler registers the handler for async drive.folder.sync jobs.
func (s *Service) RegisterDriveFolderSyncHandler(jobsSvc *appjobs.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(appjobs.TypeDriveFolderSync, s.HandleDriveFolderSyncJob)
		s.log.Info("registered drive.folder.sync job handler")
	}
}

// HandleDriveFolderSyncJob processes an async drive.folder.sync job.
// It wraps SyncFolderID with progress reporting and mutex serialization.
func (s *Service) HandleDriveFolderSyncJob(ctx context.Context, job *appjobs.Job, tools *appjobs.JobTools) (map[string]any, error) {
	var payload struct {
		DriveFolderID string `json:"drive_folder_id"`
		Source        string `json:"source,omitempty"`
		Name          string `json:"name,omitempty"`
		MediaType     string `json:"media_type,omitempty"`
	}
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

// resolveRepo returns the assets.ClipsRepository for the given source name.
// Falls back to s.defaultClipsRepo (set from the first configured target)
// for ad-hoc sources like "drive" that don't have a pre-configured target.
func (s *Service) resolveRepo(source string) *assets.ClipsRepository {
	for _, t := range s.targets {
		if strings.EqualFold(t.Source, source) {
			return t.Repo
		}
	}
	return s.defaultClipsRepo
}
