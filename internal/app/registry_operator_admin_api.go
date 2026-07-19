package app

import (
	"fmt"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	operatorapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/operator"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
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

	desc, err := operatorapi.Build(operatorapi.Dependencies{
		AssetService: root.Repos.Assets,
		JobService:   root.Jobs.Facade,
		OutboxPort:   nil, // optional — outbox monitoring degrades gracefully
		AllowedRoots: allowedRoots,
	}, log)
	if err != nil {
		return fmt.Errorf("wire registry: operator-admin-api build: %w", err)
	}

	if err := tryRegisterModuleStrict(registry, log, desc, WithRegistrationPoint("register.OperatorConsole")); err != nil {
		return fmt.Errorf("wire registry: operator-admin-api module: %w", err)
	}
	return nil
}
