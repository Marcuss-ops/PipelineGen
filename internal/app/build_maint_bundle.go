package app

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
)

// BuildMaintBundle constructs the periodic maintenance + deletion services.
func BuildMaintBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, drive *DriveBundle, repos *RepoBundle, search *SearchBundle, jobs *JobsBundle, outboxBundle *OutboxBundle) (*MaintBundle, error) {
	_ = ctx
	deletionSvc := deletion.NewDeletionService(
		repos.ClipsRepo, repos.ClipsRepo, repos.ClipsRepo,
		repos.VoiceoverRepo, repos.ImageRepo,
		drive.DriveUploader, search.AssetTreeService, search.AssetIndexService,
		outboxBundle.Dispatcher,
		log,
	)
	maintenanceSvc := maintenance.NewService(cfg, log,
		search.AssetIndexService, search.AssetTreeService, deletionSvc,
		jobs.Service, dbs.main.DB,
	)
	if err := maintenanceSvc.RegisterHandler(); err != nil {
		log.Warn("failed to register maintenance handler", zap.Error(err))
	}

	return &MaintBundle{
		MaintenanceSvc: maintenanceSvc,
		DeletionSvc:    deletionSvc,
	}, nil
}
