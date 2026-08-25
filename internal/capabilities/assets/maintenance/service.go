package maintenance

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/deletion"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assetindex"
)

// DriveFileChecker is the minimal interface needed by deep_cleanup.
type DriveFileChecker interface {
	FileIsNotTrashed(ctx context.Context, fileID string) (bool, error)
}

// Service coordinates system-wide maintenance tasks. Required dependencies are
// fixed at construction time; optional cleanup tuning remains explicit.
type Service struct {
	cfg            *config.Config
	log            *zap.Logger
	assetIndexSvc  *assetindex.Service
	assetTreeSvc   *assettree.Service
	deletionSvc    *deletion.DeletionService
	jobsSvc        *appjobs.Service
	driveFileCheck DriveFileChecker
	repos          []assets.MaintenanceRepository

	// deepCleanupBatch caps how many rows per pass to keep the maintenance
	// job from monopolising the DB. Zero means use the default.
	deepCleanupBatch int
}

// NewService creates a maintenance service with all required collaborators.
func NewService(
	cfg *config.Config,
	log *zap.Logger,
	assetIndexSvc *assetindex.Service,
	assetTreeSvc *assettree.Service,
	deletionSvc *deletion.DeletionService,
	jobsSvc *appjobs.Service,
	repos ...assets.MaintenanceRepository,
) *Service {
	return &Service{
		cfg:           cfg,
		log:           log,
		assetIndexSvc: assetIndexSvc,
		assetTreeSvc:  assetTreeSvc,
		deletionSvc:   deletionSvc,
		jobsSvc:       jobsSvc,
		repos:         repos,
	}
}

// SetDriveFileChecker wires the optional Drive-side existence check used by
// deep_cleanup. Leave nil to skip the Drive orphan pass.
func (s *Service) SetDriveFileChecker(c DriveFileChecker) {
	s.driveFileCheck = c
}

// SetDeepCleanupBatch overrides the per-pass row cap (default 1000).
func (s *Service) SetDeepCleanupBatch(n int) {
	if n > 0 {
		s.deepCleanupBatch = n
	}
}

// HandleJob processes system maintenance jobs.
func (s *Service) HandleJob(ctx context.Context, job *appjobs.Job, tools *appjobs.JobTools) (map[string]any, error) {
	s.log.Info("Handling maintenance job", zap.String("job_id", job.ID))

	var payload struct {
		Deep   bool `json:"deep"`
		DryRun bool `json:"dry_run"`
	}
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal maintenance payload: %w", err)
		}
	}

	if tools.Progress != nil {
		tools.Progress(10, "Starting system maintenance")
	}

	results, err := s.RunCleanup(ctx, payload.Deep, payload.DryRun)
	if err != nil {
		return nil, err
	}

	if tools.Progress != nil {
		tools.Progress(100, "System maintenance completed")
	}

	return results, nil
}

// RegisterHandler registers the maintenance job handler.
func (s *Service) RegisterHandler() error {
	if s.jobsSvc != nil {
		if err := s.jobsSvc.RegisterHandler(appjobs.TypeSystemCleanup, appjobs.HandlerFunc(s.HandleJob)); err != nil {
			return err
		}
		s.log.Info("Registered system maintenance job handler")
	}
	return nil
}
