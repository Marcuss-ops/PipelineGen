// Package app — build_bundles_artlist_publishers.go
//
// Artlist delivery-side wiring. The repo bundle below groups the
// SQLite-backed asset lifecycle repos (Processing/Version) + the
// composition-root adapters that wrap the artlist-pkg port concretes
// (Runs / DownloadAudit) + the license/release/rendition compliance
// repos consumed by handlers and audit tooling.
//
// godlike/06 SSOT: every repo / adapter below is the canonical
// SOLE constructor site for that port. The artlist-pkg composition
// receives them verbatim (no shim layer between the composition root
// and the artlist.NewService.ServiceDeps surface).
//
// godlike/07 fail-closed: an error from any New* constructor
// propagates verbatim with the "WireArtlist: " prefix (preserved
// for grep-compat with the orchestrator's existing observability
// pipeline — WireArtlist does NOT distinguish helper-source errors).
package app

import (
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"

	artlist "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	artlistsql "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/artlist"
	"go.uber.org/zap"
)

// constructArtlistRepositories returns the publisher-side bundle built
// from the wiring.ArtlistBundle's DB handle. Each error is wrapped with the
// canonical "WireArtlist: " prefix so existing log-grep pipelines and
// e2e test assertions (tests/e2e/artlist_full_run_test.go) continue to
// match verbatim.
//
// PR-DEADC-ARTLIST-ASSET-LOC-REPO-RETIRE (2026-07-10): AssetLocRepo
// retired per `architecture/action-plans/2026-07-10-dead-code-p1-p2-cleanup.md#§3-Phase-C`.
// assetSQLiteStore.LocationRepository() remains available for future
// callers but is no longer wired into the artlist service (zero
// call sites in service.go after retirement). godlike/06 SSOT:
// the canonical adapter factories still live on
// *assets.AssetStoreSQLite.ProcessingRepository() / .VersionRepository()
// (processing_queries.go / version_queries.go).
//
// PR-ARTLIST-PERSIST-FIX (2026-07-04): mandatory RunRepository wiring
// (godlike/07 fail-closed) via the composition-root adapter. The artlist
// runs adapter (internal/app/artlist_runs_adapter.go) holds the SINGLE
// compile-time pin to artlist.RunRepository (mirrors the ClipsRepository
// precedent: no cycle, adapter in composition root).
//
// PR-HLS-AES128 / P0 (July 2026): download audit repository for rate-limit
// and compliance tracking. Constructed from the same media.db.sqlite
// handle and bridged to the artlist port via the composition-root
// adapter (internal/app/artlist_download_audit_adapter.go).
func constructArtlistRepositories(
	bundle *wiring.ArtlistBundle,
	log *zap.Logger,
) (artlistRepositories, error) {
	assetSQLiteStore := assets.NewAssetStoreSQLite(bundle.DB.DB, log)

	artlistRunsRepo, err := artlistsql.NewArtlistRunsRepository(bundle.DB.DB, log)
	if err != nil {
		return artlistRepositories{}, fmt.Errorf("WireArtlist: NewArtlistRunsRepository: %w", err)
	}
	artlistRunsAdapter := NewArtlistRunsRepoAdapter(artlistRunsRepo)
	_ = (artlist.RunRepository)(artlistRunsAdapter) // compile-time pin surface

	artlistDownloadAuditRepo, err := artlistsql.NewArtlistDownloadAuditRepository(bundle.DB.DB, log)
	if err != nil {
		return artlistRepositories{}, fmt.Errorf("WireArtlist: NewArtlistDownloadAuditRepository: %w", err)
	}
	artlistDownloadAuditAdapter := NewArtlistDownloadAuditAdapter(artlistDownloadAuditRepo)
	// artlist.DownloadAuditRepository compile-time pin lives in
	// artlist_download_audit_adapter.go (the constructor returns the
	// interface directly so an extra cast here would be a no-op).

	licenseRepo, err := assets.NewAssetLicenseRepository(bundle.DB.DB, log)
	if err != nil {
		return artlistRepositories{}, fmt.Errorf("WireArtlist: NewAssetLicenseRepository: %w", err)
	}
	releaseRepo, err := assets.NewAssetReleaseRepository(bundle.DB.DB, log)
	if err != nil {
		return artlistRepositories{}, fmt.Errorf("WireArtlist: NewAssetReleaseRepository: %w", err)
	}
	renditionRepo, err := assets.NewAssetRenditionRepository(bundle.DB.DB, log)
	if err != nil {
		return artlistRepositories{}, fmt.Errorf("WireArtlist: NewAssetRenditionRepository: %w", err)
	}

	return artlistRepositories{
		AssetProcRepo:        assetSQLiteStore.ProcessingRepository(),
		AssetVerRepo:         assetSQLiteStore.VersionRepository(),
		RunsAdapter:          artlistRunsAdapter,
		DownloadAuditAdapter: artlistDownloadAuditAdapter,
		LicenseRepo:          licenseRepo,
		ReleaseRepo:          releaseRepo,
		RenditionRepo:        renditionRepo,
	}, nil
}
