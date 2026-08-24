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
	"net/http"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	sqliteassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// BuildMaintBundle constructs the periodic maintenance + deletion services.
func BuildMaintBundle(ctx context.Context, cfg *config.Config, dbs *wiring.Databases, log *zap.Logger, drive *wiring.DriveBundle, repos *wiring.RepoBundle, search *wiring.SearchBundle, jobs *wiring.JobsBundle, outboxBundle *wiring.OutboxBundle, sourceCatalog *artifacts.SourceCatalog) (*wiring.MaintBundle, error) {
	_ = ctx
	// PR-WAVE-1-DRIVE-SSOT (July 2026): the driveUploader arg is
	// REMOVED from the canonical ctor — the field has been retired
	// from DeletionService (the value was unused by every service
	// method; the canonical async Drive surface is the dispatcher
	// outbox port per godlike/06 SSOT one-canonical-owner-per-fact).
	deletionSvc := deletion.NewDeletionService(deletion.DeletionServiceDeps{
		Repos: deletion.DeletionRepoDeps{
			ArtlistRepo:   repos.ClipsRepo,
			ClipsRepo:     repos.ClipsRepo,
			StockRepo:     repos.ClipsRepo,
			VoiceoverRepo: repos.VoiceoverRepo,
			ImagesRepo:    repos.ImageRepo,
		},
		Catalog: sourceCatalog,
		Index: deletion.DeletionIndexDeps{
			AssetTreeSvc:  search.AssetTreeService,
			AssetIndexSvc: search.AssetIndexService,
		},
		Dispatcher: outboxBundle.Dispatcher,
		Log:        log,
	})
	maintRepo := sqliteassets.NewMaintenanceRepository(dbs.DualPool.Writer, log)
	maintenanceSvc := maintenance.NewService(cfg, log,
		search.AssetIndexService, search.AssetTreeService, deletionSvc,
		jobs.Service, maintRepo,
	)

	recertificationConfig := entitycatalog.DefaultRecertificationConfig()
	if cfg != nil {
		if parsed, err := time.ParseDuration(cfg.Jobs.EntityImageRecertificationInterval); err == nil && parsed > 0 {
			recertificationConfig.Interval = parsed
		}
		if cfg.Jobs.EntityImageRecertificationBatchSize > 0 {
			recertificationConfig.BatchSize = cfg.Jobs.EntityImageRecertificationBatchSize
		}
	}
	entityImageRecertification := entitycatalog.NewRecertificationService(
		repos.EntityImageCatalog,
		entitycatalog.NewHTTPImageCandidateValidator(&http.Client{Timeout: 30 * time.Second}),
		recertificationConfig,
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

	return &wiring.MaintBundle{
		MaintenanceSvc:             maintenanceSvc,
		DeletionSvc:                deletionSvc,
		EntityImageRecertification: entityImageRecertification,
	}, nil
}
