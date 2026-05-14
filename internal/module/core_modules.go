package module

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/api/handlers/common"
	drivehandler "velox/go-master/internal/api/handlers/drive"
	scraperhandler "velox/go-master/internal/api/handlers/scraper"
	"velox/go-master/internal/api/handlers/system"
	"velox/go-master/pkg/config"
)

// SystemModule handles system diagnostic routes
type SystemModule struct {
	BaseModule
	cfg     *config.Config
	log     *zap.Logger
	handler *system.Handler
}

// NewSystemModule creates a new system module
func NewSystemModule(cfg *config.Config, log *zap.Logger) *SystemModule {
	return &SystemModule{
		BaseModule: *NewBaseModule("system", func(cfg *config.Config) bool { return true }),
		cfg:        cfg,
		log:        log,
		handler:    system.NewHandler(cfg, log),
	}
}

// RegisterRoutes registers system routes
func (m *SystemModule) RegisterRoutes(rg *gin.RouterGroup) {
	systemGroup := rg.Group("/system")
	{
		systemGroup.GET("/doctor", m.handler.Doctor)
	}
}

// UtilityModule is a registrable module for Utility functionality (internal endpoints)
type UtilityModule struct {
	BaseModule
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
		BaseModule: *NewBaseModule("utility", func(cfg *config.Config) bool { return handler != nil }),
		cfg:        cfg,
		log:        log,
		handler:    handler,
	}
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
