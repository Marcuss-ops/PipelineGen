package module

import (
	"context"
	
	"velox/go-master/internal/api/handlers/jobs"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/pkg/config"
	
	"github.com/gin-gonic/gin"
	
	"go.uber.org/zap"
)

// JobsModule is a registrable module for Jobs functionality
type JobsModule struct {
	cfg     *config.Config
	log     *zap.Logger
	service *jobservice.Service
	handler *jobs.Handler
}

// NewJobsModule creates a new Jobs module
func NewJobsModule(
	cfg *config.Config,
	log *zap.Logger,
	service *jobservice.Service,
	handler *jobs.Handler,
) *JobsModule {
	return &JobsModule{
		cfg:     cfg,
		log:     log,
		service: service,
		handler: handler,
	}
}

// Name returns the module name
func (m *JobsModule) Name() string {
	return "jobs"
}

// Enabled checks if this module is enabled
func (m *JobsModule) Enabled(cfg *config.Config) bool {
	// Jobs are always enabled
	return true
}

// RegisterRoutes registers the module's routes
func (m *JobsModule) RegisterRoutes(rg *gin.RouterGroup) {
	if m.handler == nil {
		m.log.Warn("jobs handler is nil, skipping route registration")
		return
	}

	group := rg.Group("/jobs")
	m.handler.RegisterRoutes(group)
}

// Start performs startup tasks
func (m *JobsModule) Start(ctx context.Context) error {
	m.log.Info("starting jobs module")
	// Jobs service starts workers if needed
	return nil
}

// Stop performs graceful shutdown
func (m *JobsModule) Stop(ctx context.Context) error {
	m.log.Info("stopping jobs module")
	// Cleanup if needed
	return nil
}
