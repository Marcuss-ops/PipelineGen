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
	if root.Repos == nil || root.Repos.Assets == nil {
		return fmt.Errorf("wire registry: operator-admin-api: asset service not available")
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
		AssetService:    root.Repos.Assets,
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
