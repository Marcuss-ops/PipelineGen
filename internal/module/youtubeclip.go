package module

import (
	"context"
	
	youtubeclipHandler "velox/go-master/internal/api/handlers/youtubeclip"
	youtubeclipService "velox/go-master/internal/service/youtubeclip"
	"velox/go-master/pkg/config"
	
	"github.com/gin-gonic/gin"
	
	"go.uber.org/zap"
)

// YouTubeClipModule is a registrable module for YouTube clip functionality
type YouTubeClipModule struct {
	cfg     *config.Config
	log     *zap.Logger
	service *youtubeclipService.Service
	handler *youtubeclipHandler.Handler
}

// NewYouTubeClipModule creates a new YouTubeClip module
func NewYouTubeClipModule(
	cfg *config.Config,
	log *zap.Logger,
	service *youtubeclipService.Service,
	handler *youtubeclipHandler.Handler,
) *YouTubeClipModule {
	return &YouTubeClipModule{
		cfg:     cfg,
		log:     log,
		service: service,
		handler: handler,
	}
}

// Name returns the module name
func (m *YouTubeClipModule) Name() string {
	return "youtube-clips"
}

// Enabled checks if this module is enabled
func (m *YouTubeClipModule) Enabled(cfg *config.Config) bool {
	return cfg.Features.YouTubeEnabled
}

// RegisterRoutes registers the module's routes
func (m *YouTubeClipModule) RegisterRoutes(rg *gin.RouterGroup) {
	if m.handler == nil {
		m.log.Warn("youtube clip handler is nil, skipping route registration")
		return
	}
	m.handler.RegisterRoutes(rg)
}

// Start performs startup tasks
func (m *YouTubeClipModule) Start(ctx context.Context) error {
	m.log.Info("starting YouTube clips module")
	// No background tasks needed for now
	return nil
}

// Stop performs graceful shutdown
func (m *YouTubeClipModule) Stop(ctx context.Context) error {
	m.log.Info("stopping YouTube clips module")
	// No cleanup needed for now
	return nil
}
