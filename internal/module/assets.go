package module

import (
	"context"

	"github.com/gin-gonic/gin"

	"go.uber.org/zap"

	"velox/go-master/internal/api/handlers/assets"
	"velox/go-master/internal/repository/catalog"
	artlistSvc "velox/go-master/internal/service/artlist"
	"velox/go-master/pkg/config"
)

// AssetsModule handles unified asset search
type AssetsModule struct {
	cfg     *config.Config
	log     *zap.Logger
	handler *assets.Handler
}

// NewAssetsModule creates a new assets module
func NewAssetsModule(cfg *config.Config, log *zap.Logger, artlistSvc *artlistSvc.Service, catalogRepo *catalog.Repository) *AssetsModule {
	mod := &AssetsModule{
		cfg: cfg,
		log: log,
	}
	// Initialize handler with dependencies
	if artlistSvc != nil || catalogRepo != nil {
		mod.handler = assets.NewHandler(artlistSvc, catalogRepo, log)
	}
	return mod
}

// Name returns the module name
func (m *AssetsModule) Name() string {
	return "assets"
}

// Enabled checks if the module is enabled
func (m *AssetsModule) Enabled(cfg *config.Config) bool {
	return m.handler != nil
}

// RegisterRoutes registers asset routes
func (m *AssetsModule) RegisterRoutes(rg *gin.RouterGroup) {
	assets := rg.Group("/assets")
	{
		assets.GET("/search", m.handler.Search)
	}
}

// Start performs async startup tasks
func (m *AssetsModule) Start(ctx context.Context) error {
	m.log.Info("Assets module started")
	return nil
}

// Stop performs graceful shutdown
func (m *AssetsModule) Stop(ctx context.Context) error {
	m.log.Info("Assets module stopped")
	return nil
}
