package maintenance

import (
	"context"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
	"velox/go-master/internal/service/assetindex"
	"velox/go-master/internal/service/assettree"
	"velox/go-master/internal/service/media"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/models"
)

// Service coordinates system-wide maintenance tasks.
type Service struct {
	cfg             *config.Config
	log             *zap.Logger
	assetIndexSvc   *assetindex.Service
	assetTreeSvc    *assettree.Service
	deletionSvc     *media.DeletionService
	jobsSvc         *jobservice.Service
}

// NewService creates a new maintenance service.
func NewService(
	cfg *config.Config,
	log *zap.Logger,
	assetIndexSvc *assetindex.Service,
	assetTreeSvc *assettree.Service,
	deletionSvc *media.DeletionService,
	jobsSvc *jobservice.Service,
) *Service {
	return &Service{
		cfg:           cfg,
		log:           log,
		assetIndexSvc: assetIndexSvc,
		assetTreeSvc:  assetTreeSvc,
		deletionSvc:   deletionSvc,
		jobsSvc:       jobsSvc,
	}
}

// RunCleanup performs a full system cleanup.
func (s *Service) RunCleanup(ctx context.Context, deep bool, dryRun bool) (map[string]any, error) {
	s.log.Info("Starting system-wide cleanup", zap.Bool("deep", deep), zap.Bool("dry_run", dryRun))
	
	results := make(map[string]any)
	
	// 1. Orphan file cleanup
	assetsDir := filepath.Join(s.cfg.Storage.DataDir, s.cfg.Storage.AssetsDir)
	deleted, err := s.deletionSvc.CleanupOrphanFiles(ctx, assetsDir, dryRun)
	if err != nil {
		s.log.Error("Orphan file cleanup failed", zap.Error(err))
		results["orphan_cleanup_error"] = err.Error()
	} else {
		results["orphan_files_deleted"] = deleted
	}

	// 2. Asset Tree / Index consistency check
	if deep {
		// Implement deep consistency checks if needed
		s.log.Info("Deep consistency check requested (not fully implemented yet)")
	}

	return results, nil
}

// HandleJob processes system maintenance jobs.
func (s *Service) HandleJob(ctx context.Context, job *models.Job, tools *jobservice.JobTools) (map[string]any, error) {
	s.log.Info("Handling maintenance job", zap.String("job_id", job.ID))

	if tools.Progress != nil {
		tools.Progress(10, "Starting system maintenance")
	}

	deep := strings.Contains(string(job.Payload), "\"deep\":true")
	dryRun := strings.Contains(string(job.Payload), "\"dry_run\":true")

	results, err := s.RunCleanup(ctx, deep, dryRun)
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
