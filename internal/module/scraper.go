package module

import (
	"context"

	scraperhandler "velox/go-master/internal/api/handlers/scraper"
	"velox/go-master/pkg/config"

	"github.com/gin-gonic/gin"

	"go.uber.org/zap"
)

// ScraperModule is a registrable module for Scraper functionality
type ScraperModule struct {
	cfg     *config.Config
	log     *zap.Logger
	handler *scraperhandler.Handler
}

// NewScraperModule creates a new Scraper module
func NewScraperModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *scraperhandler.Handler,
) *ScraperModule {
	return &ScraperModule{
		cfg:     cfg,
		log:     log,
		handler: handler,
	}
}

// Name returns the module name
func (m *ScraperModule) Name() string {
	return "scraper"
}

// Enabled checks if this module is enabled
func (m *ScraperModule) Enabled(cfg *config.Config) bool {
	return m.handler != nil
}

// RegisterRoutes registers the module's routes
func (m *ScraperModule) RegisterRoutes(rg *gin.RouterGroup) {
	if m.handler == nil {
		m.log.Warn("scraper handler is nil, skipping route registration")
		return
	}

	group := rg.Group("/scraper")
	m.handler.RegisterRoutes(group)
}

// Start performs startup tasks
func (m *ScraperModule) Start(ctx context.Context) error {
	m.log.Info("starting scraper module")
	return nil
}

// Stop performs graceful shutdown
func (m *ScraperModule) Stop(ctx context.Context) error {
	m.log.Info("stopping scraper module")
	return nil
}
