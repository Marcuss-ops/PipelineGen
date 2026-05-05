package module

import (
	"context"

	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/pkg/config"

	"github.com/gin-gonic/gin"

	"go.uber.org/zap"
)

// UtilityModule is a registrable module for Utility functionality (internal endpoints)
type UtilityModule struct {
	cfg     *config.Config
	log     *zap.Logger
	handler *common.UtilityHandler
}

// NewUtilityModule creates a new Utility module
func NewUtilityModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *common.UtilityHandler,
) *UtilityModule {
	return &UtilityModule{
		cfg:     cfg,
		log:     log,
		handler: handler,
	}
}

// Name returns the module name
func (m *UtilityModule) Name() string {
	return "utility"
}

// Enabled checks if this module is enabled
func (m *UtilityModule) Enabled(cfg *config.Config) bool {
	// Utility endpoints are always enabled (they're internal)
	return m.handler != nil
}

// RegisterRoutes registers the module's routes
func (m *UtilityModule) RegisterRoutes(rg *gin.RouterGroup) {
	if m.handler == nil {
		m.log.Warn("utility handler is nil, skipping route registration")
		return
	}

	group := rg.Group("/internal")
	group.GET("/slug", m.handler.Slugify)
}

// Start performs startup tasks
func (m *UtilityModule) Start(ctx context.Context) error {
	m.log.Info("starting utility module")
	return nil
}

// Stop performs graceful shutdown
func (m *UtilityModule) Stop(ctx context.Context) error {
	m.log.Info("stopping utility module")
	return nil
}
