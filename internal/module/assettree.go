package module

import (
	"context"

	assettreehandler "velox/go-master/internal/api/handlers/assettree"
	"velox/go-master/pkg/config"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// AssetTreeModule is a registrable module for Asset Tree functionality
type AssetTreeModule struct {
	cfg     *config.Config
	log     *zap.Logger
	handler *assettreehandler.Handler
}

// NewAssetTreeModule creates a new Asset Tree module
func NewAssetTreeModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *assettreehandler.Handler,
) *AssetTreeModule {
	return &AssetTreeModule{
		cfg:     cfg,
		log:     log,
		handler: handler,
	}
}

// Name returns the module name
func (m *AssetTreeModule) Name() string {
	return "assettree"
}

// Enabled checks if this module is enabled
func (m *AssetTreeModule) Enabled(cfg *config.Config) bool {
	return m.handler != nil
}

// RegisterRoutes registers the module's routes
func (m *AssetTreeModule) RegisterRoutes(rg *gin.RouterGroup) {
	if m.handler == nil {
		m.log.Warn("asset tree handler is nil, skipping route registration")
		return
	}

	group := rg.Group("/assets")
	m.handler.RegisterRoutes(group)
}

// Start performs startup tasks
func (m *AssetTreeModule) Start(ctx context.Context) error {
	m.log.Info("starting assettree module")
	return nil
}

// Stop performs graceful shutdown
func (m *AssetTreeModule) Stop(ctx context.Context) error {
	m.log.Info("stopping assettree module")
	return nil
}
