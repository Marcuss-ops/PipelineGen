package app

import (
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/system"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"go.uber.org/zap"
)

// SystemWiring holds the System module wiring.
type SystemWiring struct {
	Module module.Module
}

// WireSystem creates the System handler and module.
//
// PR4b (June 2026): narrow bundle surface. Takes only (cfg, log) and no
// *CoreDeps — the System module has zero downstream service dependencies.
// WireRegistry calls this signature directly.
func WireSystem(cfg *config.Config, log *zap.Logger) *SystemWiring {
	mod := system.NewModule(cfg, log)
	log.Info("created System module")
	return &SystemWiring{Module: mod}
}
