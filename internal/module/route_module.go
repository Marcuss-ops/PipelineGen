package module

import (
	"context"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/pkg/config"
)

// RouteModule is a generic module for simple route-only modules.
// It eliminates boilerplate for modules that just register routes.
type RouteModule struct {
	name        string
	enabled     func(*config.Config) bool
	prefix      string
	handler     interface{ RegisterRoutes(*gin.RouterGroup) }
	log         *zap.Logger
	startFn     func(context.Context) error
	stopFn      func(context.Context) error
}

// RouteModuleOption configures a RouteModule.
type RouteModuleOption func(*RouteModule)

// WithStart sets an optional start function.
func WithStart(fn func(context.Context) error) RouteModuleOption {
	return func(m *RouteModule) { m.startFn = fn }
}

// WithStop sets an optional stop function.
func WithStop(fn func(context.Context) error) RouteModuleOption {
	return func(m *RouteModule) { m.stopFn = fn }
}

// NewRouteModule creates a new RouteModule.
func NewRouteModule(
	name string,
	enabled func(*config.Config) bool,
	prefix string,
	handler interface{ RegisterRoutes(*gin.RouterGroup) },
	log *zap.Logger,
	opts ...RouteModuleOption,
) *RouteModule {
	m := &RouteModule{
		name:    name,
		enabled: enabled,
		prefix:  prefix,
		handler: handler,
		log:     log,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Name returns the module name.
func (m *RouteModule) Name() string {
	return m.name
}

// Enabled checks if this module is enabled.
func (m *RouteModule) Enabled(cfg *config.Config) bool {
	if m.enabled == nil {
		return m.handler != nil
	}
	return m.enabled(cfg)
}

// RegisterRoutes registers the module's routes.
func (m *RouteModule) RegisterRoutes(rg *gin.RouterGroup) {
	if m.handler == nil {
		m.log.Warn("handler is nil, skipping route registration", zap.String("module", m.name))
		return
	}
	group := rg.Group(m.prefix)
	m.handler.RegisterRoutes(group)
}

// Start performs startup tasks.
func (m *RouteModule) Start(ctx context.Context) error {
	if m.startFn != nil {
		return m.startFn(ctx)
	}
	m.log.Info("starting module", zap.String("module", m.name))
	return nil
}

// Stop performs graceful shutdown.
func (m *RouteModule) Stop(ctx context.Context) error {
	if m.stopFn != nil {
		return m.stopFn(ctx)
	}
	m.log.Info("stopping module", zap.String("module", m.name))
	return nil
}
