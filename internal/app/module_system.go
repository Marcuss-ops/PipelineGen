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
func WireSystem(cfg *config.Config, log *zap.Logger) *SystemWiring {
	mod := system.NewModule(cfg, log)
	log.Info("created System module")
	return &SystemWiring{Module: mod}
}
