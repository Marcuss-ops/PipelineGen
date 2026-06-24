// Package api provides HTTP API routing for the PipelineGen system.
package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	common "github.com/Marcuss-ops/PipelineGen/internal/api/common"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	remoteshared "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/remote/shared"
	"go.uber.org/zap"
)

// Router holds the API router configuration
type Router struct {
	cfg                 *config.Config
	rateLimitMiddleware *middleware.RateLimitMiddleware
	registry            *Registry
	workerHandler       interface{ RegisterRoutes(*gin.RouterGroup) }
	ctx                 context.Context
	healthSvc           interface{}                   // *systemhealth.Service; typed as interface{} to avoid import coupling
	readyChecker        *systemhealth.ReadyChecker    // codex/health-ready-contract: concrete type, not interface{}
}

// NewRouter creates a new API router
func NewRouter(cfg *config.Config) *Router {
	return &Router{
		cfg: cfg,
	}
}

// SetRegistry sets the module registry for route registration
func (r *Router) SetRegistry(reg *Registry) {
	r.registry = reg
}

// SetWorkerHandler wires internal worker routes into the router.
func (r *Router) SetWorkerHandler(h interface{ RegisterRoutes(*gin.RouterGroup) }) {
	r.workerHandler = h
}

// SetContext sets the context for module lifecycle management
func (r *Router) SetContext(ctx context.Context) {
	r.ctx = ctx
}

// SetHealthService wires the application-layer health.Service into the router.
// The concrete type is *systemhealth.Service but the field is interface{}
// so this file stays free of infrastructure imports (PR1 Health boundary, June 2026).
func (r *Router) SetHealthService(svc interface{}) {
	r.healthSvc = svc
}

// SetReadyChecker wires the application-layer ReadyChecker into the router.
// codex/health-ready-contract (June 2026): previously ReadyChecker was silently
// nil in Setup(), making /ready always return 503.
func (r *Router) SetReadyChecker(rc *systemhealth.ReadyChecker) {
	r.readyChecker = rc
}

// buildCORSConfig builds a CORS configuration from the application security settings.
// If no origins are configured, cross-origin requests are blocked entirely.
func buildCORSConfig(cfg *config.Config) cors.Config {
	corsCfg := cors.Config{
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Velox-Admin-Token"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: false,
		MaxAge:           12 * time.Hour,
	}

	origins := cfg.Security.CORSOrigins

	// Require explicit CORS origins - default closed
	if len(origins) == 0 {
		// No origins configured - block all cross-origin requests
		corsCfg.AllowOrigins = []string{}
		return corsCfg
	}

	if len(origins) == 1 && origins[0] == "*" {
		corsCfg.AllowAllOrigins = true
		return corsCfg
	}

	corsCfg.AllowOrigins = origins
	return corsCfg
}

// Setup configures and returns the gin engine with all middleware, static routes,
// health endpoints, and dynamically registered module routes.
func (r *Router) Setup() *gin.Engine {
	log := zap.L().Named("router")
	gin.SetMode(r.cfg.Server.GinMode)

	engine := gin.New()

	// Global middleware
	engine.Use(middleware.RequestID())
	engine.Use(middleware.Logger())
	engine.Use(middleware.Recovery())
	engine.Use(gzip.Gzip(gzip.DefaultCompression))

	// Root redirect to health
	engine.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/health")
	})

	// Only add CORS middleware if origins are configured
	corsConfig := buildCORSConfig(r.cfg)
	if len(corsConfig.AllowOrigins) > 0 || corsConfig.AllowAllOrigins {
		engine.Use(cors.New(corsConfig))
	} else {
		log.Info("CORS disabled - no origins configured")
	}

	// Unified health check (PR1, June 2026): single /health with ?deep=true
	// for aggregated DB+Drive+Qdrant+JobBroker checks. The health service
	// lives in ComposeRoot.Utility.HealthService and is wired via
	// SetHealthService before Setup() runs.
	// codex/health-ready-contract (June 2026): ReadyChecker is now wired
	// via SetReadyChecker — /ready no longer receives nil in production.
	var healthHandler *common.HealthHandler
	if r.healthSvc != nil {
		if svc, svcOk := r.healthSvc.(*systemhealth.Service); svcOk {
			healthHandler = common.NewHealthHandler(svc, r.readyChecker)
		}
	}
	if healthHandler == nil {
		log.Warn("health service not wired, health endpoints will return 503")
		healthHandler = common.NewHealthHandler(nil, nil /* nil-by-design; integration stub only */)
	}
	engine.GET("/health", healthHandler.Health)
	engine.GET("/ready", healthHandler.Ready)

	// Prometheus metrics endpoint — protected if METRICS_AUTH_TOKEN is set
	metricsHandler := gin.WrapH(promhttp.Handler())
	if token := os.Getenv("METRICS_AUTH_TOKEN"); token != "" {
		engine.GET("/metrics", func(c *gin.Context) {
			if c.GetHeader("Authorization") != "Bearer "+token {
				c.AbortWithStatus(http.StatusUnauthorized)
				return
			}
			metricsHandler(c)
		})
	} else {
		engine.GET("/metrics", metricsHandler)
	}

	// Serve static assets (images, etc.)
	assetsDir := filepath.Join(r.cfg.Storage.DataDir, "assets")
	engine.Static("/assets", assetsDir)
	engine.Static("/media/google-accounting", r.cfg.GoogleAccounting.DownloadDir)

	// API routes
	api := engine.Group("/api")
	{
		// Protected routes — Auth + RateLimit + WorkspaceScope
		protected := api.Group("")
		protected.Use(middleware.Auth(r.cfg))
		r.rateLimitMiddleware = middleware.RateLimit(r.cfg)
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

	internalGroup := engine.Group(remoteshared.InternalPathPrefix)
	internalGroup.Use(middleware.WorkerAuth(r.cfg))
	{
		if r.workerHandler != nil {
			r.workerHandler.RegisterRoutes(internalGroup)
		}
	}

	// Log all registered routes
	for _, route := range engine.Routes() {
		log.Info("registered route", zap.String("method", route.Method), zap.String("path", route.Path))
	}

	return engine
}

// Stop cleans up resources used by the router (rate limiter goroutines)
func (r *Router) Stop() {
	if r.rateLimitMiddleware != nil {
		r.rateLimitMiddleware.Stop()
	}
}
