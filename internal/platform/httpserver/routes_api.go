package httpserver

import (
	"github.com/gin-gonic/gin"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/middleware"
)

// registerAPIRoutes wires the public /api surface: the protected subtree
// (Auth + RateLimit + WorkspaceScope) that hosts the module registry for
// the media, jobs and other modules. Returns the /api group so Setup()
// can mount /api/capabilities AFTER the WireRegistry is built (the wire
// surface must reflect the full routed surface).
func (r *Router) registerAPIRoutes(engine *gin.Engine, log *zap.Logger) *gin.RouterGroup {
	// API routes
	api := engine.Group("/api")
	{
		// Protected routes — Auth + RateLimit + WorkspaceScope
		protected := api.Group("")
		protected.Use(middleware.Auth(r.cfg.Auth, r.cfg.Log))
		r.rateLimitMiddleware = middleware.RateLimit(r.cfg.Rate, middleware.NewOSEnvReader())
		protected.Use(r.rateLimitMiddleware.Handler)
		protected.Use(middleware.WorkspaceScopeMiddleware())
		{
			// Use module registry for route registration
			if r.registry != nil {
				log.Info("using module registry for route registration")
				r.registry.RegisterAllRoutes(protected)
			} else {
				log.Warn("no module registry available, no routes registered")
			}
		}
	}
	return api
}
