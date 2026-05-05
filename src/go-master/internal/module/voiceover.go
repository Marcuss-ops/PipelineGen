package module

import (
	"context"

	"velox/go-master/internal/api/handlers/voiceover"
	"velox/go-master/pkg/config"

	"github.com/gin-gonic/gin"

	"go.uber.org/zap"
)

// VoiceoverModule is a registrable module for Voiceover functionality
type VoiceoverModule struct {
	cfg           *config.Config
	log           *zap.Logger
	handler       *voiceover.Handler
	syncHandler   *voiceover.SyncHandler
}

// NewVoiceoverModule creates a new Voiceover module
func NewVoiceoverModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *voiceover.Handler,
	syncHandler *voiceover.SyncHandler,
) *VoiceoverModule {
	return &VoiceoverModule{
		cfg:         cfg,
		log:         log,
		handler:     handler,
		syncHandler: syncHandler,
	}
}

// Name returns the module name
func (m *VoiceoverModule) Name() string {
	return "voiceover"
}

// Enabled checks if this module is enabled
func (m *VoiceoverModule) Enabled(cfg *config.Config) bool {
	return m.handler != nil || m.syncHandler != nil
}

// RegisterRoutes registers the module's routes
func (m *VoiceoverModule) RegisterRoutes(rg *gin.RouterGroup) {
	if m.handler == nil && m.syncHandler == nil {
		m.log.Warn("voiceover handlers are nil, skipping route registration")
		return
	}

	group := rg.Group("/voiceover")
	
	if m.handler != nil {
		m.handler.RegisterRoutes(group)
	}
	if m.syncHandler != nil {
		m.syncHandler.RegisterRoutes(group)
	}
}

// Start performs startup tasks
func (m *VoiceoverModule) Start(ctx context.Context) error {
	m.log.Info("starting voiceover module")
	return nil
}

// Stop performs graceful shutdown
func (m *VoiceoverModule) Stop(ctx context.Context) error {
	m.log.Info("stopping voiceover module")
	return nil
}
