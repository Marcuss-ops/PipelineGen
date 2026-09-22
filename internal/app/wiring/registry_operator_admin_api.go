package wiring

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/operator"
	operatorapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/operator"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	operatorverify "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/verification"
	"go.uber.org/zap"
)

// registerOperatorAdminAPI wires and registers the operator admin API module.
// This module provides admin-facing read-only endpoints consumed by the
// React admin UI under /admin/. Routes are mounted under /api/assets/operator/.
//
// PG-M2M (Sep 2026): it also builds the M2M media-read surface from the SAME
// read model and publishes it on wiring.M2MMediaHandler, so a remote submitter
// with a media.read-scoped M2M key can see the media SSOT elements without
// admin credentials. The read model is constructed once here and shared by
// both surfaces — no second projection can drift.
func registerOperatorAdminAPI(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot, wiring *RegistryWiring) error {
	assetReader := root.MediaAssetReader()
	if assetReader == nil {
		// Skip, do not abort boot: the operator console is a media-read surface,
		// and the SQLite-backed reader it used to fall back to was retired
		// (MEDIA-SSOT P2-9 — serving media_assets from the operational mirror is
		// the split-brain the cutover removed). With the media SSOT closed the
		// module is simply not registered, which is the honest degraded outcome;
		// returning an error here would make a media-disabled deployment
		// unbootable, and returning the SQLite store would be a divergent
		// catalog. Same shape as the MediaIngest skip below.
		log.Warn("wire registry: operator-admin-api skipped — media asset reader unavailable (media PostgreSQL plane not deployed)")
		return nil
	}
	if root.Jobs == nil || root.Jobs.Facade == nil {
		return fmt.Errorf("wire registry: operator-admin-api: job service not available")
	}

	allowedRoots := []string{}
	if cfg.Storage.DataDir != "" {
		allowedRoots = append(allowedRoots, cfg.Storage.AbsDataDir())
	}

	// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-20): the operator inventory
	// read model (Content Library, Asset Inspector, filter facets) is answered
	// from the PostgreSQL media SSOT — NEVER the operational SQLite mirror.
	// The retired operatorread package read lifecycle_state/index_state/
	// embedding_json/metadata_json from a database the canonical committer
	// never populates, so a freshly committed asset was invisible to the
	// console and the facet counts described the mirror. When the media plane
	// is closed the handle is nil and the port stays unwired, so the handler
	// returns 503 per request (godlike/07 fail-closed) instead of serving a
	// divergent catalog.
	var readModel operator.AssetInventoryReader
	if root.MediaPostgres != nil {
		readModel = pgmedia.NewOperatorInventoryReader(root.MediaPostgres, log)
	}

	// PG-M2M (Sep 2026): publish the M2M media-read surface from the same
	// reader. The group is mounted by the server composition on
	// /api/v1/media behind JobClientAuthMiddleware + RequireScope(media.read).
	// Enabled closure is true so the routes exist whenever the media SSOT
	// read model is wired; the EnableM2M() gate + the media.read scope live
	// inside the middleware.
	if wiring != nil {
		wiring.M2MMediaHandler = operatorapi.NewM2MModule(readModel, log, func() bool { return true })
		log.Info("created M2M media read module (GET /assets, /assets/:id, /facets on /api/v1/media)")
	}

	var verifier operator.IndexVerifier
	if root.Process != nil && root.Process.QdrantClient != nil {
		verifier = operatorverify.NewOperatorIndexVerifier(root.Process.QdrantClient)
	}

	desc, err := operatorapi.Build(operatorapi.Dependencies{
		AssetService:    assetReader,
		ReadModel:       readModel,
		IndexVerifier:   verifier,
		JobService:      root.Jobs.Facade,
		OutboxPort:      nil, // optional — outbox monitoring degrades gracefully
		Mutator:         root.Outbox.Dispatcher,
		OperatorOptions: &operatorapi.OperatorOptions{AllowedRoots: allowedRoots},
	}, log)
	if err != nil {
		return fmt.Errorf("wire registry: operator-admin-api build: %w", err)
	}

	if err := tryRegisterModuleStrict(registry, log, desc, WithRegistrationPoint("register.OperatorConsole")); err != nil {
		return fmt.Errorf("wire registry: operator-admin-api module: %w", err)
	}
	return nil
}
