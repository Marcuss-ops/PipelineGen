package module

import (
	"context"

	"velox/go-master/internal/api/handlers/script/handlers"
	"velox/go-master/internal/api/middleware"
	"velox/go-master/pkg/config"

	"github.com/gin-gonic/gin"

	"go.uber.org/zap"
)

// ScriptHistoryModule is a registrable module for Script History functionality
type ScriptHistoryModule struct {
	cfg     *config.Config
	log     *zap.Logger
	handler *handlers.ScriptHistoryHandler
}

// NewScriptHistoryModule creates a new ScriptHistory module
func NewScriptHistoryModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *handlers.ScriptHistoryHandler,
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
func (m *ScriptHistoryModule) Enabled(cfg *config.Config) bool {
	return cfg.Features.ScriptClipsEnabled
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

// Start performs startup tasks
func (m *ScriptHistoryModule) Start(ctx context.Context) error {
	m.log.Info("starting script history module")
	return nil
}

// Stop performs graceful shutdown
func (m *ScriptHistoryModule) Stop(ctx context.Context) error {
	m.log.Info("stopping script history module")
	return nil
}
