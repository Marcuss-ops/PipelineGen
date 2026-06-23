package script

import (
	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"

	"github.com/gin-gonic/gin"

	"go.uber.org/zap"
)

// ScriptHistoryModule is a registrable module for Script History functionality
type ScriptHistoryModule struct {
	cfg     *config.Config
	log     *zap.Logger
	handler *ScriptHistoryHandler
}

// NewScriptHistoryModule creates a new ScriptHistory module
func NewScriptHistoryModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *ScriptHistoryHandler,
) *ScriptHistoryModule {
	return &ScriptHistoryModule{
		cfg:     cfg,
		log:     log,
		handler: handler,
	}
}

// Name returns the module name
func (m *ScriptHistoryModule) Name() string {
	return "scripts"
}

// Enabled checks if this module is enabled
func (m *ScriptHistoryModule) Enabled() bool {
	return m.cfg.Features.ScriptClipsEnabled
}

// RegisterRoutes registers the module's routes
func (m *ScriptHistoryModule) RegisterRoutes(rg *gin.RouterGroup) {
	if m.handler == nil {
		m.log.Warn("script history handler is nil, skipping route registration")
		return
	}

	group := rg.Group("/scripts")
	group.Use(middleware.ScriptClipsEnabled(m.cfg))
	m.handler.RegisterRoutes(group)
}
