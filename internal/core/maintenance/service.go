package maintenance

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"

	"go.uber.org/zap"
	"velox/go-master/internal/media/assetindex"
	"velox/go-master/internal/media/assettree"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media"
	"velox/go-master/internal/config"
	"velox/go-master/internal/media/models"
)

// Service coordinates system-wide maintenance tasks.
type Service struct {
	cfg           *config.Config
	log           *zap.Logger
	assetIndexSvc *assetindex.Service
	assetTreeSvc  *assettree.Service
	deletionSvc   *media.DeletionService
	jobsSvc       *jobservice.Service
	db            *sql.DB
}

// NewService creates a new maintenance service.
func NewService(
	cfg *config.Config,
	log *zap.Logger,
	assetIndexSvc *assetindex.Service,
	assetTreeSvc *assettree.Service,
	deletionSvc *media.DeletionService,
	jobsSvc *jobservice.Service,
	db *sql.DB,
) *Service {
	return &Service{
		cfg:           cfg,
		log:           log,
		assetIndexSvc: assetIndexSvc,
		assetTreeSvc:  assetTreeSvc,
		deletionSvc:   deletionSvc,
		jobsSvc:       jobsSvc,
		db:            db,
	}
}

// SetDeletionService updates the deletion service.
func (s *Service) SetDeletionService(deletionSvc *media.DeletionService) {
	s.deletionSvc = deletionSvc
}

// RunCleanup performs a full system cleanup.
func (s *Service) RunCleanup(ctx context.Context, deep bool, dryRun bool) (map[string]any, error) {
	s.log.Info("Starting system-wide cleanup", zap.Bool("deep", deep), zap.Bool("dry_run", dryRun))

	results := make(map[string]any)

	if s.deletionSvc == nil {
		s.log.Error("Deletion service not available for cleanup")
		return nil, fmt.Errorf("deletion service not initialized")
	}

	// 1. Orphan file cleanup
	assetsDir := s.cfg.Storage.AssetsPath()
	deleted, err := s.deletionSvc.CleanupOrphanFiles(ctx, assetsDir, dryRun)
	if err != nil {
		s.log.Error("Orphan file cleanup failed", zap.Error(err))
		results["orphan_cleanup_error"] = err.Error()
	} else {
		results["orphan_files_deleted"] = deleted
	}

	// 2. Asset Tree / Index consistency check
	if deep {
		s.log.Info("Deep consistency check started")
		// Deep consistency checks are not yet implemented.
		// Planned checks:
		//   - Detect DB entries whose local file no longer exists.
		//   - Detect DB entries with missing or invalid Drive links.
		//   - Reconcile Asset Index records against the Asset Tree.
		results["deep_cleanup"] = "partially_implemented"
	}

	// 3. Stale temp files cleanup
	tempDir := s.cfg.Storage.TempPath()
	if _, err := os.Stat(tempDir); err == nil {
		// Basic temp cleanup logic could go here
		results["temp_cleanup"] = "skipped"
	}

	// 4. API request logs retention cleanup (retention logic)
	if s.db != nil && !dryRun {
		s.log.Info("Running API request logs retention cleanup", zap.Int("days", s.cfg.Jobs.RetentionDays))
		retentionQuery := fmt.Sprintf("DELETE FROM api_requests WHERE ts < datetime('now', '-%d days')", s.cfg.Jobs.RetentionDays)
		res, err := s.db.ExecContext(ctx, retentionQuery)
		if err != nil {
			s.log.Error("API request logs retention cleanup failed", zap.Error(err))
			results["api_requests_cleanup_error"] = err.Error()
		} else {
			rowsAffected, _ := res.RowsAffected()
			s.log.Info("API request logs retention cleanup completed", zap.Int64("rows_deleted", rowsAffected))
			results["api_requests_deleted"] = rowsAffected

			// Run optimized vacuum if rows were deleted
			if rowsAffected > 0 {
				s.log.Info("Running incremental space reclamation after pruning API requests")
				// Try incremental vacuum first (requires PRAGMA auto_vacuum = INCREMENTAL to be set on DB)
				if _, err := s.db.ExecContext(ctx, "PRAGMA incremental_vacuum(500)"); err != nil {
					s.log.Warn("Incremental vacuum failed, falling back to full VACUUM", zap.Error(err))
					if _, err := s.db.ExecContext(ctx, "VACUUM"); err != nil {
						s.log.Warn("Full VACUUM failed", zap.Error(err))
					}
				}
			}
		}
	}

	return results, nil
}

// HandleJob processes system maintenance jobs.
func (s *Service) HandleJob(ctx context.Context, job *models.Job, tools *jobservice.JobTools) (map[string]any, error) {
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
func (s *Service) RegisterHandler() {
	if s.jobsSvc != nil {
		s.jobsSvc.RegisterHandler(models.JobTypeSystemCleanup, s.HandleJob)
		s.log.Info("Registered system maintenance job handler")
	}
}
