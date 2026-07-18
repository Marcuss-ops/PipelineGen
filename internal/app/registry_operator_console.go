package app

import (
	"fmt"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	operatorapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/operator"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

// registerOperatorConsole wires and registers the operator console API module.
// This module provides admin-facing read-only endpoints consumed by the
// operator console binary (cmd/operator-console). Routes are under /api/operator/.
func registerOperatorConsole(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot) error {
	if root.Repos == nil || root.Repos.Assets == nil {
		return fmt.Errorf("wire registry: operator-console: asset service not available")
	}
	if root.Jobs == nil || root.Jobs.Facade == nil {
		return fmt.Errorf("wire registry: operator-console: job service not available")
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
		return fmt.Errorf("wire registry: operator-console build: %w", err)
	}

	if err := tryRegisterModuleStrict(registry, log, desc, WithRegistrationPoint("register.OperatorConsole")); err != nil {
		return fmt.Errorf("wire registry: operator-console module: %w", err)
	}
	return nil
}
