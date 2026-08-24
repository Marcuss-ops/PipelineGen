package httpserver

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// RouteModule is a generic module for simple route-only modules.
// It eliminates boilerplate for modules that just register routes.
// Lifecycle (Start/Stop) is managed by the composition root, not by the module.
type RouteModule struct {
	name       string
	enabled    func() bool
	prefix     string
	handler    interface{ RegisterRoutes(*gin.RouterGroup) }
	log        *zap.Logger
	middleware []gin.HandlerFunc
}

// RouteModuleOption configures a RouteModule.
type RouteModuleOption func(*RouteModule)

// WithMiddleware adds middleware to the module's route group.
func WithMiddleware(mw ...gin.HandlerFunc) RouteModuleOption {
	return func(m *RouteModule) { m.middleware = append(m.middleware, mw...) }
}

// NewRouteModule creates a new RouteModule.
// enabled is a closure that bakes the config check at construction time.
func NewRouteModule(
	name string,
	enabled func() bool,
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
func (m *RouteModule) Enabled() bool {
	if m.enabled == nil {
		return m.handler != nil
	}
	return m.enabled()
}

// RegisterRoutes registers the module's routes.
func (m *RouteModule) RegisterRoutes(rg *gin.RouterGroup) {
	if m.handler == nil {
		m.log.Warn("handler is nil, skipping route registration", zap.String("module", m.name))
		return
	}
	group := rg.Group(m.prefix)
	for _, mw := range m.middleware {
		group.Use(mw)
	}
	m.handler.RegisterRoutes(group)
}
