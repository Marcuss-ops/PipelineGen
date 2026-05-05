package module

import (
	"context"

	"velox/go-master/internal/api/handlers/script/handlers"
	"velox/go-master/internal/api/middleware"
	"velox/go-master/pkg/config"

	"github.com/gin-gonic/gin"

	"go.uber.org/zap"
)

// ScriptDocsModule is a registrable module for Script Docs functionality
type ScriptDocsModule struct {
	cfg     *config.Config
	log     *zap.Logger
	handler *handlers.ScriptDocsHandler
}

// NewScriptDocsModule creates a new ScriptDocs module
func NewScriptDocsModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *handlers.ScriptDocsHandler,
) *ScriptDocsModule {
	return &ScriptDocsModule{
		cfg:     cfg,
		log:     log,
		handler: handler,
	}
}

// Name returns the module name
func (m *ScriptDocsModule) Name() string {
	return "script-docs"
}

// Enabled checks if this module is enabled
func (m *ScriptDocsModule) Enabled(cfg *config.Config) bool {
	return cfg.Features.ScriptDocsEnabled
}

// RegisterRoutes registers the module's routes
func (m *ScriptDocsModule) RegisterRoutes(rg *gin.RouterGroup) {
	if m.handler == nil {
		m.log.Warn("script docs handler is nil, skipping route registration")
		return
	}

	group := rg.Group("/script-docs")
	group.Use(middleware.ScriptDocsEnabled(m.cfg))
	m.handler.RegisterRoutes(group)
}

// Start performs startup tasks
func (m *ScriptDocsModule) Start(ctx context.Context) error {
	m.log.Info("starting script docs module")
	return nil
}

// Stop performs graceful shutdown
func (m *ScriptDocsModule) Stop(ctx context.Context) error {
	m.log.Info("stopping script docs module")
	return nil
}
