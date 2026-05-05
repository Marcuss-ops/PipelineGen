package module

import (
	"context"

	drivehandler "velox/go-master/internal/api/handlers/drive"
	"velox/go-master/pkg/config"

	"github.com/gin-gonic/gin"

	"go.uber.org/zap"
)

// DriveModule is a registrable module for Drive functionality
type DriveModule struct {
	cfg     *config.Config
	log     *zap.Logger
	handler *drivehandler.Handler
}

// NewDriveModule creates a new Drive module
func NewDriveModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *drivehandler.Handler,
) *DriveModule {
	return &DriveModule{
		cfg:     cfg,
		log:     log,
		handler: handler,
	}
}

// Name returns the module name
func (m *DriveModule) Name() string {
	return "drive"
}

// Enabled checks if this module is enabled
func (m *DriveModule) Enabled(cfg *config.Config) bool {
	return cfg.Features.DriveEnabled
}

// RegisterRoutes registers the module's routes
func (m *DriveModule) RegisterRoutes(rg *gin.RouterGroup) {
	if m.handler == nil {
		m.log.Warn("drive handler is nil, skipping route registration")
		return
	}

	group := rg.Group("/drive")
	m.handler.RegisterRoutes(group)
}

// Start performs startup tasks
func (m *DriveModule) Start(ctx context.Context) error {
	m.log.Info("starting drive module")
	return nil
}

// Stop performs graceful shutdown
func (m *DriveModule) Stop(ctx context.Context) error {
	m.log.Info("stopping drive module")
	return nil
}
