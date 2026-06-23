package system

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
)

// Module handles system diagnostic routes.
type Module struct {
	name    string
	cfg     *config.Config
	log     *zap.Logger
	handler *SystemHandler
}

// NewModule creates a new system module.
func NewModule(cfg *config.Config, log *zap.Logger, toolChecker appassets.ToolChecker, processRunner appassets.ProcessRunner, dbHealthChecker appassets.DBHealthChecker) *Module {
	return &Module{
		name:    "system",
		cfg:     cfg,
		log:     log,
		handler: NewSystemHandler(cfg, log, toolChecker, processRunner, dbHealthChecker),
	}
}

// Name returns the module name.
func (m *Module) Name() string { return m.name }

// Enabled always returns true for the system module.
func (m *Module) Enabled() bool { return true }

// RegisterRoutes registers system routes.
func (m *Module) RegisterRoutes(rg *gin.RouterGroup) {
	systemGroup := rg.Group("/system")
	{
		systemGroup.GET("/doctor", m.handler.Doctor)
	}
}
