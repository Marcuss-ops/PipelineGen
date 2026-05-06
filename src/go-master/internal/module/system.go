package module

import (
	"context"

	"github.com/gin-gonic/gin"

	"go.uber.org/zap"

	"velox/go-master/internal/api/handlers/system"
	"velox/go-master/pkg/config"
)

// SystemModule handles system diagnostic routes
type SystemModule struct {
	cfg     *config.Config
	log     *zap.Logger
	handler *system.Handler
}

// NewSystemModule creates a new system module
func NewSystemModule(cfg *config.Config, log *zap.Logger) *SystemModule {
	return &SystemModule{
		cfg:     cfg,
		log:     log,
		handler: system.NewHandler(cfg, log),
	}
}

// Name returns the module name
func (m *SystemModule) Name() string {
	return "system"
}

// Enabled checks if the module is enabled
func (m *SystemModule) Enabled(cfg *config.Config) bool {
	return true // System module is always enabled
}

// RegisterRoutes registers system routes
func (m *SystemModule) RegisterRoutes(rg *gin.RouterGroup) {
	system := rg.Group("/system")
	{
		system.GET("/doctor", m.handler.Doctor)
	}
}

// Start performs async startup tasks
func (m *SystemModule) Start(ctx context.Context) error {
	m.log.Info("System module started")
	return nil
}

// Stop performs graceful shutdown
func (m *SystemModule) Stop(ctx context.Context) error {
	m.log.Info("System module stopped")
	return nil
}
