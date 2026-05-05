package module

import (
	"context"
	
	mediahandler "velox/go-master/internal/api/handlers/media"
	"velox/go-master/pkg/config"
	
	"github.com/gin-gonic/gin"
	
	"go.uber.org/zap"
)

// MediaModule is a registrable module for Media functionality
type MediaModule struct {
	cfg     *config.Config
	log     *zap.Logger
	handler *mediahandler.CommonHandler
}

// NewMediaModule creates a new Media module
func NewMediaModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *mediahandler.CommonHandler,
) *MediaModule {
	return &MediaModule{
		cfg:     cfg,
		log:     log,
		handler: handler,
	}
}

// Name returns the module name
func (m *MediaModule) Name() string {
	return "media"
}

// Enabled checks if this module is enabled
func (m *MediaModule) Enabled(cfg *config.Config) bool {
	// Media module is enabled if handler is not nil
	return m.handler != nil
}

// RegisterRoutes registers the module's routes
func (m *MediaModule) RegisterRoutes(rg *gin.RouterGroup) {
	if m.handler == nil {
		m.log.Warn("media handler is nil, skipping route registration")
		return
	}

	group := rg.Group("/media")
	m.handler.RegisterRoutes(group)
}

// Start performs startup tasks
func (m *MediaModule) Start(ctx context.Context) error {
	m.log.Info("starting media module")
	return nil
}

// Stop performs graceful shutdown
func (m *MediaModule) Stop(ctx context.Context) error {
	m.log.Info("stopping media module")
	return nil
}
