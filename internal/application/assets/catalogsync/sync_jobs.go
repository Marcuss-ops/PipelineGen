package catalogsync

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
)

// HandleJob processes a catalog.sync job.
func (s *Service) HandleJob(ctx context.Context, job *appjobs.Job, tools *appjobs.JobTools) (map[string]any, error) {
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
//
// Audit P0 #2 (cont.) — PR-VALIDATOR-LITERAL-REGISTER (July 2026): Register
// now returns error so the composition root can fail-closed at boot if the
// dispatcher rejects the binding. Pre-PR-VALIDATOR-LITERAL-REGISTER the
// silent-`if jobsSvc != nil`+log-Info path masked nil-typed-dispatcher
// + duplicate-bind failures — the same silent-success class audit-P0.2
// partially closed for the voiceover handlers and that this commit fully
// closes for all silent-Warn critical handlers.
func (s *Service) RegisterHandler(jobsSvc *appjobs.Service) error {
	if jobsSvc == nil {
		// P1 #1 (July 2026): wraps appjobs.ErrMissingDeps via %w so the
		// composition root + tests can assert via
		// errors.Is(err, appjobs.ErrMissingDeps) regardless of the
		// handler prefix.
		return fmt.Errorf("catalogsync.Service.RegisterHandler: jobsSvc is nil (composition root must wire jobs.Service before calling Register): %w", appjobs.ErrMissingDeps)
	}
	if err := jobsSvc.RegisterHandler(appjobs.TypeCatalogSync, appjobs.HandlerFunc(s.HandleJob)); err != nil {
		return fmt.Errorf("catalogsync.Service.RegisterHandler: bind %q to dispatcher: %w", appjobs.TypeCatalogSync, err)
	}
	s.log.Info("registered catalog.sync job handler")
	return nil
}

// RegisterDriveFolderSyncHandler registers the handler for async drive.folder.sync jobs.
//
// Audit P0 #2 (cont.) — PR-VALIDATOR-LITERAL-REGISTER (July 2026): same
// signature change as RegisterHandler.
func (s *Service) RegisterDriveFolderSyncHandler(jobsSvc *appjobs.Service) error {
	if jobsSvc == nil {
		// P1 #1 (July 2026): wraps appjobs.ErrMissingDeps via %w so the
		// composition root + tests can assert via
		// errors.Is(err, appjobs.ErrMissingDeps) regardless of the
		// handler prefix.
		return fmt.Errorf("catalogsync.Service.RegisterDriveFolderSyncHandler: jobsSvc is nil (composition root must wire jobs.Service before calling Register): %w", appjobs.ErrMissingDeps)
	}
	if err := jobsSvc.RegisterHandler(appjobs.TypeDriveFolderSync, appjobs.HandlerFunc(s.HandleDriveFolderSyncJob)); err != nil {
		return fmt.Errorf("catalogsync.Service.RegisterDriveFolderSyncHandler: bind %q to dispatcher: %w", appjobs.TypeDriveFolderSync, err)
	}
	s.log.Info("registered drive.folder.sync job handler")
	return nil
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

	// Resolve the repository and index-state port for the given source.
	repo := s.resolveRepo(source)
	if repo == nil {
		return nil, fmt.Errorf("no repository configured for source: %s", source)
	}
	indexer := s.resolveIndexer(source)
	if indexer == nil {
		return nil, fmt.Errorf("no asset indexer configured for source: %s", source)
	}

	if tools.Progress != nil {
		tools.Progress(10, "Scanning Drive folder recursively")
	}

	summary, err := s.SyncFolderID(ctx, payload.DriveFolderID, source, payload.Name, mediaType, repo, indexer)
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
//
// Wave G (June 2026) DECOUPLING — the legacy `s.defaultClipsRepo`
// fallback is REMOVED. Ad-hoc sources like "drive" must now be
// pre-listed in Targets; unknown sources surface an explicit error
// to the async job handler instead of silently falling back to a
// guessed repo. The targets-loop is preserved because the async
// job payload carries only a Source string (not a repo pointer) and
// the job handler must resolve the repo at execution time.
func (s *Service) resolveRepo(source string) CatalogRepository {
	for _, t := range s.targets {
		if strings.EqualFold(t.Source, source) {
			return t.Repo
		}
	}
	return nil
}

func (s *Service) resolveIndexer(source string) AssetIndexer {
	for _, t := range s.targets {
		if strings.EqualFold(t.Source, source) {
			return t.Indexer
		}
	}
	return nil
}
