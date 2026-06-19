package api

import (
	"context"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// UtilityModule is a registrable module for internal utility endpoints.
type UtilityModule struct {
	name    string
	cfg     *config.Config
	log     *zap.Logger
	handler *UtilityHandler
}

// NewUtilityModule creates a new Utility module.
func NewUtilityModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *UtilityHandler,
) *UtilityModule {
	return &UtilityModule{
		name:    "utility",
		cfg:     cfg,
		log:     log,
		handler: handler,
	}
}

// Name returns the module name.
func (m *UtilityModule) Name() string { return m.name }

// Enabled checks if this module is enabled.
func (m *UtilityModule) Enabled(*config.Config) bool { return m.handler != nil }

// RegisterRoutes registers the module's routes.
func (m *UtilityModule) RegisterRoutes(rg *gin.RouterGroup) {
	if m.handler == nil {
		m.log.Warn("utility handler is nil, skipping route registration")
		return
	}

	group := rg.Group("/internal")
	group.GET("/slug", m.handler.Slugify)
}

// Start performs startup tasks.
func (m *UtilityModule) Start(context.Context) error { return nil }

// Stop performs shutdown tasks.
func (m *UtilityModule) Stop(context.Context) error { return nil }


