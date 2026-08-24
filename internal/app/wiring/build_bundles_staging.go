// Package app — staging service bundle construction (FASE 3 Spina Dorsale,
// Push 3.1b, July 2026).
//
// Extracted from composition.go per PG-028 (the established
// `internal/app/build_bundles_<capability>.go` pattern). This file
// owns the SOLE canonical construction site for the artifact_stages
// Repository + staging.StoreService pair. No caller (worker pool,
// finalizer, admin tool, test stub) should construct a second
// instance or read the staging workspace dir independently — every
// consumer reaches the typed ports via ComposeRoot.Staging.
//
// godlike/06 SSOT: this is the SINGLE canonical wiring of the
// FASE 3 application-layer staging step. The composition root
// (composition.go::NewComposition) calls BuildStagingBundle in
// dependency order — it depends ONLY on dbs.Main.DB (for the
// artifact_stages SQLite handle) + cfg.Storage.StagingPath() (for
// the canonical workspace dir) + log — so it can be placed at the
// end of NewComposition, after BuildTextTrackBundle, without
// disturbing the existing 12-bundle aggregation.
//
// godlike/07 fail-closed: a misconfigured workspace dir (empty
// after the StorageConfig default-resolves to
// `/var/lib/pipelinegen/staging`) trips a wrapped error from
// staging.NewStoreService; the composition root surfaces the
// error and NewComposition aborts (no silent fallback to "." or
// /tmp). A nil *sql.DB trips a wrapped error from
// artifactstages.NewRepository; the composition root surfaces
// the error and NewComposition aborts (no zero-value DB).
package wiring

import (
	"fmt"

	"go.uber.org/zap"

	stagingsvc "github.com/Marcuss-ops/PipelineGen/internal/capabilities/staging"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	artifactstages "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/artifact_stages"
)

// BuildStagingBundle constructs the canonical FASE 3 Spina Dorsale
// staging bundle: the artifact_stages SQLite Repository (the
// single-writer of the artifact_stages table) + the
// application-layer staging.StoreService (the typed port the
// publisher worker pool will drain into). Returns the StagingBundle
// for assignment to ComposeRoot.Staging.
//
// Dependency surface (intentionally minimal — only 3 deps):
//   - dbs.Main.DB: the *sql.DB handle the artifact_stages Repository
//     uses for all 8 methods (Insert, GetByID, ListByJob,
//     ListByState, MarkPublished, MarkSucceeded, MarkFailedPermanent,
//     IncrementAttemptCount). dbs.Main is the same handle every
//     other repository in the composition root uses (jobs, assets,
//     voiceover, scripts, etc.) — co-location on the same handle
//     preserves the canonical "one DB per deployment" SSOT.
//   - cfg: the resolved PipelineGen config; BuildStagingBundle reads
//     cfg.Storage.StagingPath() to discover the workspace root. The
//     resolution chain (StorageConfig.StagingDir → env var
//     PIPELINEGEN_STAGING_WORKSPACE → default /var/lib/pipelinegen/staging)
//     is owned by the config layer; this constructor is a consumer
//     only.
//   - log: *zap.Logger for the boot-time wiring log line.
//
// Returns error on (a) nil dbs.Main.DB (composition-time
// misconfiguration) or (b) staging.NewStoreService rejection
// (empty workspace dir — should never trigger after
// StorageConfig.StagingPath()'s default-resolution but
// fail-closed at the boundary anyway).
func BuildStagingBundle(dbs *Databases, cfg *config.Config, log *zap.Logger) (*StagingBundle, error) {
	// Step 1: construct the artifact_stages SQLite Repository.
	// artifactstages.NewRepository is the canonical concrete
	// (internal/platform/sqlite/artifact_stages).
	// A nil *sql.DB surfaces as a wrapped error from the
	// SQLite driver on the first call; we pre-validate here to
	// fail loud at boot, not mid-job.
	if dbs == nil || dbs.DualPool == nil || dbs.DualPool.Writer == nil {
		return nil, fmt.Errorf("build staging: dbs.DualPool.Writer is nil (composition root failed to construct the DualPool before BuildStagingBundle)")
	}
	repo := artifactstages.NewRepository(dbs.DualPool.Writer)
	// Conformance with artifact.ArtifactStageRepository is pinned at the
	// StagingBundle.Repository field type + the canonical anchor
	// at internal/platform/sqlite/artifact_stages/repository.go:51
	// (`var _ artifact.Repository = (*Repository)(nil)`). The
	// compiler checks the port conformance at the
	// &StagingBundle{...} literal in this function's return.

	// Step 2: resolve the staging workspace dir via the canonical
	// StorageConfig method. StagingPath() returns
	// `/var/lib/pipelinegen/staging` if StagingDir is empty
	// (the default tag is applied at env-var load time; this
	// fallback covers tests / admin CLIs that construct the
	// struct without going through the loader).
	workspace := cfg.Storage.StagingPath()

	// Step 3: construct the application-layer StoreService. The
	// concrete is staging.NewStoreService; the field on
	// StagingBundle is typed as the Store port so test stubs
	// (fakeStore) can satisfy it via Go's implicit-interface
	// rules. The NewStoreService constructor fail-closes on
	// nil repo (impossible — repo is non-nil above) or empty
	// workspace dir (impossible — StagingPath() always returns
	// a non-empty default).
	store, err := stagingsvc.NewStoreService(repo, workspace)
	if err != nil {
		return nil, fmt.Errorf("build staging: NewStoreService (workspace=%q): %w", workspace, err)
	}
	// Conformance with staging.Store is pinned at the
	// StagingBundle.Store field type + the canonical anchor at
	// internal/application/staging/service.go:53
	// (`var _ Store = (*StoreService)(nil)`). The compiler
	// checks the port conformance at the &StagingBundle{...}
	// literal in this function's return.

	log.Info("staging bundle wired (FASE 3 Spina Dorsale Push 3.1b)",
		zap.String("workspace", workspace),
		zap.String("repository_table", "artifact_stages"),
	)

	return &StagingBundle{
		Store:      store,
		Repository: repo,
		Workspace:  workspace,
	}, nil
}
