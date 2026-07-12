// Package app — build_bundles_maint.go (split July 2026).
//
// This file owns the maintenance + deletion bundle construction.
// Extracted from build_bundles_domain.go per AGENTS.md Pattern 5.
//
// godlike/06 SSOT: BuildMaintBundle is the single canonical owner of the
// deletion + maintenance service construction.
package app

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	sqliteassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// BuildMaintBundle constructs the periodic maintenance + deletion services.
func BuildMaintBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, drive *DriveBundle, repos *RepoBundle, search *SearchBundle, jobs *JobsBundle, outboxBundle *OutboxBundle) (*MaintBundle, error) {
	_ = ctx
	deletionSvc := deletion.NewDeletionService(
		repos.ClipsRepo, repos.ClipsRepo, repos.ClipsRepo,
		repos.VoiceoverRepo, repos.ImageRepo,
		drive.driveUploader, search.AssetTreeService, search.AssetIndexService,
		outboxBundle.Dispatcher,
		nil, // driveGoneChecker (Blocco 3.1 commit 3/3 — pre-commit-4/3 wiring forward-pointer)
		nil, // completionTxRunner (Blocco 3.1 commit 3/3 — pre-commit-4/3 wiring forward-pointer)
		log,
	)
	maintRepo := sqliteassets.NewMaintenanceRepository(dbs.dualPool.Writer, log)
	maintenanceSvc := maintenance.NewService(cfg, log,
		search.AssetIndexService, search.AssetTreeService, deletionSvc,
		jobs.Service, maintRepo,
	)
	// Registries-and-SSOT (June 2026): this is the canonical site for
	// the `system.cleanup` job-type registration. Spec §"Uniqueness"
	// requires composition to fail on duplicate job types; the previous
	// log-Warn-and-continue pattern silently absorbed any second-call
	// attempt (a latent bug that manifested after WireRegistry's
	// duplicate call was removed). Propagate so any future second-call
	// path fails composition rather than masking the underlying
	// Dispatcher error.
	if err := maintenanceSvc.RegisterHandler(); err != nil {
		return nil, fmt.Errorf("compose: register maintenance job handler (BuildMaintBundle): %w", err)
	}

	return &MaintBundle{
		MaintenanceSvc: maintenanceSvc,
		DeletionSvc:    deletionSvc,
	}, nil
}
