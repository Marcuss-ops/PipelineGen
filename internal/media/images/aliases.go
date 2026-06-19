// Package images is the backward-compatibility shim for
// internal/media/images. The canonical implementation now lives in
// internal/application/images (PR-D.4 migration). Existing callers
// keep working unchanged; new code MUST import
// internal/application/images directly.
//
// Coverage mirrors every public identifier exported from the 21 source
// files (service, registry_adapter, nvidia_remote, ingest helpers,
// search helpers, google_vids_assets, …). The shim exposes the 4 exported
// types (Service, DiagnosticsReport, RemoteImageJob,
// SemanticMetadataPayload) and 2 constructors (NewService,
// NewRegistryAdapter) verbatim so that callers using concrete
// infrastructure types compile unchanged.
package images

import (
	driveapi "google.golang.org/api/drive/v3"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/artifacts"
	appimages "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/media/generation"
	clipsRepo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/clips"
	imagesRepo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/images"
)

// ── Type aliases ────────────────────────────────────────────────────

type (
	Service        = appimages.Service
	DiagnosticsReport = appimages.DiagnosticsReport
	RemoteImageJob = appimages.RemoteImageJob

	// SemanticMetadataPayload is a Go type alias of semantic.Payload in the
	// canonical package; re-export the type alias so callers see identical
	// field layout across both import paths.
	SemanticMetadataPayload = appimages.SemanticMetadataPayload
)

// ── Function re-exports ─────────────────────────────────────────────

// NewService mirrors appimages.NewService verbatim so that callers using
// concrete infrastructure types (`*config.Config`,
// `*imagesRepo.Repository`, `*clipsRepo.Repository`, `*driveapi.Service`,
// `*generation.StyleRegistry`, `*zap.Logger`) compile unchanged through
// the shim.
func NewService(
	cfg *config.Config,
	repo *imagesRepo.Repository,
	stockRepo *clipsRepo.Repository,
	driveSvc *driveapi.Service,
	styleRegistry *generation.StyleRegistry,
	log *zap.Logger,
) *Service {
	return appimages.NewService(cfg, repo, stockRepo, driveSvc, styleRegistry, log)
}

// NewRegistryAdapter mirrors appimages.NewRegistryAdapter verbatim.
// Return type is `artifacts.Registry`; callers using the concrete
// adapter interface pass through without compile-time surprises.
func NewRegistryAdapter(repo *imagesRepo.Repository, imagesDir string, log *zap.Logger) artifacts.Registry {
	return appimages.NewRegistryAdapter(repo, imagesDir, log)
}
