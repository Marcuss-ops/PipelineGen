package module

import (
	"context"

	imghandler "velox/go-master/internal/api/handlers/images"
	"velox/go-master/pkg/config"

	"github.com/gin-gonic/gin"

	"go.uber.org/zap"
)

// ImagesModule is a registrable module for Images functionality
type ImagesModule struct {
	cfg     *config.Config
	log     *zap.Logger
	handler *imghandler.Handler
}

// NewImagesModule creates a new Images module
func NewImagesModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *imghandler.Handler,
) *ImagesModule {
	return &ImagesModule{
		cfg:     cfg,
		log:     log,
		handler: handler,
	}
}

// Name returns the module name
func (m *ImagesModule) Name() string {
	return "images"
}

// Enabled checks if this module is enabled
func (m *ImagesModule) Enabled(cfg *config.Config) bool {
	return m.handler != nil
}

// RegisterRoutes registers the module's routes
func (m *ImagesModule) RegisterRoutes(rg *gin.RouterGroup) {
	if m.handler == nil {
		m.log.Warn("images handler is nil, skipping route registration")
		return
	}

	group := rg.Group("/images")
	m.handler.RegisterRoutes(group)
}

// Start performs startup tasks
func (m *ImagesModule) Start(ctx context.Context) error {
	m.log.Info("starting images module")
	return nil
}

// Stop performs graceful shutdown
func (m *ImagesModule) Stop(ctx context.Context) error {
	m.log.Info("stopping images module")
	return nil
}
