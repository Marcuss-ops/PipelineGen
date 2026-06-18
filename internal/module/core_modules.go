package module

import (
	"context"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api/handlers/common"
	drivehandler "github.com/Marcuss-ops/PipelineGen/internal/api/handlers/drive"
	scraperhandler "github.com/Marcuss-ops/PipelineGen/internal/api/handlers/scraper"
	"github.com/Marcuss-ops/PipelineGen/internal/api/handlers/system"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// SystemModule handles system diagnostic routes.
type SystemModule struct {
	name    string
	cfg     *config.Config
	log     *zap.Logger
	handler *system.Handler
}

// NewSystemModule creates a new system module.
func NewSystemModule(cfg *config.Config, log *zap.Logger) *SystemModule {
	return &SystemModule{
		name:    "system",
		cfg:     cfg,
		log:     log,
		handler: system.NewHandler(cfg, log),
	}
}

// Name returns the module name.
func (m *SystemModule) Name() string { return m.name }

// Enabled always returns true for the system module.
func (m *SystemModule) Enabled(*config.Config) bool { return true }

// RegisterRoutes registers system routes.
func (m *SystemModule) RegisterRoutes(rg *gin.RouterGroup) {
	systemGroup := rg.Group("/system")
	{
		systemGroup.GET("/doctor", m.handler.Doctor)
	}
}

// Start performs startup tasks.
func (m *SystemModule) Start(context.Context) error { return nil }

// Stop performs shutdown tasks.
func (m *SystemModule) Stop(context.Context) error { return nil }

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

// NewScraperModule creates a new Scraper module using RouteModule.
func NewScraperModule(
	log *zap.Logger,
	handler *scraperhandler.Handler,
) *RouteModule {
	return NewRouteModule(
		"scraper",
		nil, // always enabled if wired
		"/scraper",
		handler,
		log,
	)
}

// NewDriveModule creates a new Drive module using RouteModule.
func NewDriveModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *drivehandler.Handler,
) *RouteModule {
	return NewRouteModule(
		"drive",
		func(cfg *config.Config) bool { return cfg.Features.DriveEnabled },
		"/drive",
		handler,
		log,
	)
}
