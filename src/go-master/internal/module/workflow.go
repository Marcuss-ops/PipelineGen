package module

import (
	"context"

	workflowhandler "velox/go-master/internal/api/handlers/workflow"
	"velox/go-master/internal/api/middleware"
	"velox/go-master/pkg/config"

	"github.com/gin-gonic/gin"

	"go.uber.org/zap"
)

// WorkflowModule is a registrable module for Workflow functionality
type WorkflowModule struct {
	cfg     *config.Config
	log     *zap.Logger
	handler *workflowhandler.Handler
}

// NewWorkflowModule creates a new Workflow module
func NewWorkflowModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *workflowhandler.Handler,
) *WorkflowModule {
	return &WorkflowModule{
		cfg:     cfg,
		log:     log,
		handler: handler,
	}
}

// Name returns the module name
func (m *WorkflowModule) Name() string {
	return "workflows"
}

// Enabled checks if this module is enabled
func (m *WorkflowModule) Enabled(cfg *config.Config) bool {
	return m.handler != nil && cfg.Features.WorkflowEnabled
}

// RegisterRoutes registers the module's routes with feature flag middleware
func (m *WorkflowModule) RegisterRoutes(rg *gin.RouterGroup) {
	if m.handler == nil {
		m.log.Warn("workflow handler is nil, skipping route registration")
		return
	}

	group := rg.Group("/workflows")
	// Apply feature flag middleware to protect all workflow routes
	group.Use(middleware.WorkflowEnabled(m.cfg))
	m.handler.RegisterRoutes(group)
}

// Start performs startup tasks
func (m *WorkflowModule) Start(ctx context.Context) error {
	m.log.Info("starting workflow module")
	return nil
}

// Stop performs graceful shutdown
func (m *WorkflowModule) Stop(ctx context.Context) error {
	m.log.Info("stopping workflow module")
	return nil
}
