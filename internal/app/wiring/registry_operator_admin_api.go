package wiring

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/operator"
	operatorapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/operator"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	operatorverify "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/verification"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/operatorread"
	"go.uber.org/zap"
)

// registerOperatorAdminAPI wires and registers the operator admin API module.
// This module provides admin-facing read-only endpoints consumed by the
// React admin UI under /admin/. Routes are mounted under /api/assets/operator/.
func registerOperatorAdminAPI(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot) error {
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

	var readModel operator.AssetInventoryReader
	if root.DB != nil {
		readModel = operatorread.NewInventoryReader(root.DB.DB, log)
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
