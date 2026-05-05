package module

import (
	"context"
	
	artlistHandler "velox/go-master/internal/api/handlers/artlist"
	"velox/go-master/internal/api/middleware"
	artlistService "velox/go-master/internal/service/artlist"
	"velox/go-master/pkg/config"
	
	"github.com/gin-gonic/gin"
	
	"go.uber.org/zap"
)

// ArtlistModule is a registrable module for Artlist functionality
type ArtlistModule struct {
	cfg     *config.Config
	log     *zap.Logger
	service *artlistService.Service
	handler *artlistHandler.Handler
}

// NewArtlistModule creates a new Artlist module
func NewArtlistModule(
	cfg *config.Config,
	log *zap.Logger,
	service *artlistService.Service,
	handler *artlistHandler.Handler,
) *ArtlistModule {
	return &ArtlistModule{
		cfg:     cfg,
		log:     log,
		service: service,
		handler: handler,
	}
}

// Name returns the module name
func (m *ArtlistModule) Name() string {
	return "artlist"
}

// Enabled checks if this module is enabled
func (m *ArtlistModule) Enabled(cfg *config.Config) bool {
	return cfg.Features.ArtlistEnabled
}

// RegisterRoutes registers the module's routes
func (m *ArtlistModule) RegisterRoutes(rg *gin.RouterGroup) {
	if m.handler == nil {
		m.log.Warn("artlist handler is nil, skipping route registration")
		return
	}

	group := rg.Group("/artlist")
	group.Use(middleware.ArtlistEnabled(m.cfg))
	m.handler.RegisterRoutes(group)
}

// Start performs startup tasks
func (m *ArtlistModule) Start(ctx context.Context) error {
	m.log.Info("starting artlist module")
	// No background tasks needed for now
	return nil
}

// Stop performs graceful shutdown
func (m *ArtlistModule) Stop(ctx context.Context) error {
	m.log.Info("stopping artlist module")
	if m.service != nil {
		return m.service.Close()
	}
	return nil
}
