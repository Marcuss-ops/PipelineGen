package maintenance

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/media"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assettree"
)

// DriveFileChecker is the minimal interface we need from the Drive uploader
// for deep_cleanup. Decoupling lets us pass a mock in tests and avoids an
// import cycle on internal/upload/drive from internal/core.
type DriveFileChecker interface {
	FileIsNotTrashed(ctx context.Context, fileID string) (bool, error)
}

// Service coordinates system-wide maintenance tasks.
//
// Package layout (split out from a 584-line god object):
//   - service.go        — Service struct + constructors + HandleJob + RegisterHandler.
//   - run_cleanup.go    — RunCleanup orchestrator + DB optimisation per pass.
//   - deep_cleanup.go   — orphan detection (local file existence + Drive Files.Get).
//   - temp_cleanup.go   — stale temp-file sweep.
//
// RunCleanup itself is documented phase-by-phase in run_cleanup.go.
type Service struct {
	cfg            *config.Config
	log            *zap.Logger
	assetIndexSvc  *assetindex.Service
	assetTreeSvc   *assettree.Service
	deletionSvc    *media.DeletionService
	jobsSvc        *jobservice.Service
	driveFileCheck DriveFileChecker
	dbs            []*sql.DB
	// deepCleanupBatch caps how many rows per pass to keep the maintenance
	// job from monopolising the DB. Zero means use the default.
	deepCleanupBatch int
}

// NewService creates a new maintenance service.
func NewService(
	cfg *config.Config,
	log *zap.Logger,
	assetIndexSvc *assetindex.Service,
	assetTreeSvc *assettree.Service,
	deletionSvc *media.DeletionService,
	jobsSvc *jobservice.Service,
	dbs ...*sql.DB,
) *Service {
	return &Service{
		cfg:           cfg,
		log:           log,
		assetIndexSvc: assetIndexSvc,
		assetTreeSvc:  assetTreeSvc,
		deletionSvc:   deletionSvc,
		jobsSvc:       jobsSvc,
		dbs:           dbs,
	}
}

// SetDeletionService updates the deletion service.
func (s *Service) SetDeletionService(deletionSvc *media.DeletionService) {
	s.deletionSvc = deletionSvc
}

// SetDriveFileChecker wires the Drive-side existence check used by
// deep_cleanup. Optional: leave nil to skip the Drive orphan pass.
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
func (s *Service) HandleJob(ctx context.Context, job *jobservice.Job, tools *jobservice.JobTools) (map[string]any, error) {
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
		if err := s.jobsSvc.RegisterHandler(jobservice.JobTypeSystemCleanup, s.HandleJob); err != nil {
			return err
		}
		s.log.Info("Registered system maintenance job handler")
	}
	return nil
}
