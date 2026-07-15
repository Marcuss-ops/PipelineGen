// Package app owns maintenance and deletion bundle construction.
package app

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	sqliteassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

func BuildMaintBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, drive *DriveBundle, repos *RepoBundle, search *SearchBundle, jobs *JobsBundle, outboxBundle *OutboxBundle) (*MaintBundle, error) {
	_ = ctx
	_ = drive

	deletionSvc := deletion.NewDeletionService(deletion.Dependencies{
		Repositories: deletion.RepositoryPorts{
			Artlist:   repos.ClipsRepo,
			Clips:     repos.ClipsRepo,
			Stock:     repos.ClipsRepo,
			Voiceover: repos.VoiceoverRepo,
			Images:    repos.ImageRepo,
		},
		Mutation: deletion.MutationPorts{
			Dispatcher: outboxBundle.Dispatcher,
			AssetTree:  search.AssetTreeService,
		},
		Completion: deletion.CompletionPorts{},
		Maintenance: deletion.MaintenancePorts{
			AssetIndex: search.AssetIndexService,
		},
		Log: log,
	})

	maintRepo := sqliteassets.NewMaintenanceRepository(dbs.dualPool.Writer, log)
	maintenanceSvc := maintenance.NewService(
		cfg,
		log,
		search.AssetIndexService,
		search.AssetTreeService,
		deletionSvc,
		jobs.Service,
		maintRepo,
	)
	if err := maintenanceSvc.RegisterHandler(); err != nil {
		return nil, fmt.Errorf("compose: register maintenance job handler (BuildMaintBundle): %w", err)
	}
	return &MaintBundle{MaintenanceSvc: maintenanceSvc, DeletionSvc: deletionSvc}, nil
}
