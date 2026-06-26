package api

import (
	common "github.com/Marcuss-ops/PipelineGen/internal/api/common"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// UtilityModule is a registrable module for internal utility endpoints.
type UtilityModule struct {
	name    string
	cfg     *config.Config
	log     *zap.Logger
	handler *common.UtilityHandler
}

// NewUtilityModule creates a new Utility module.
func NewUtilityModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *common.UtilityHandler,
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
func (m *UtilityModule) Enabled() bool { return m.handler != nil }

// RegisterRoutes registers the module's routes.
func (m *UtilityModule) RegisterRoutes(rg *gin.RouterGroup) {
	if m.handler == nil {
		m.log.Warn("utility handler is nil, skipping route registration")
		return
	}

	group := rg.Group("/internal")
	group.GET("/slug", m.handler.Slugify)
}
