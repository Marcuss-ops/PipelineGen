// Package api provides HTTP API routing for the PipelineGen system.
package api

import (
	"context"
	"net/http"
	"path/filepath"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"

	"go.uber.org/zap"

	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/api/middleware"
	"velox/go-master/internal/module"
	"velox/go-master/internal/config"
)

// Router holds the API router configuration
type Router struct {
	cfg                 *config.Config
	rateLimitMiddleware *middleware.RateLimitMiddleware
	registry            *module.Registry
	ctx                 context.Context
}

// NewRouter creates a new API router
func NewRouter(cfg *config.Config) *Router {
	return &Router{
		cfg: cfg,
	}
}

// SetRegistry sets the module registry for route registration
func (r *Router) SetRegistry(reg *module.Registry) {
	r.registry = reg
}

// SetContext sets the context for module lifecycle management
func (r *Router) SetContext(ctx context.Context) {
	r.ctx = ctx
}

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

// Setup configures the gin router
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

	// Health checks (public — no auth/rate limit)
	engine.GET("/health", (&common.HealthHandler{}).Health)
	engine.GET("/api/health", (&common.HealthHandler{}).Health)

	// Serve static assets (images, etc.)
	assetsDir := filepath.Join(r.cfg.Storage.DataDir, "assets")
	engine.Static("/assets", assetsDir)

	// Serve React admin frontend
	adminDir := "web-admin/dist"
	engine.Static("/admin", adminDir)
	engine.StaticFile("/admin", filepath.Join(adminDir, "index.html"))

	// API routes
	api := engine.Group("/api")
	{
		// Protected routes — Auth + RateLimit + WorkspaceScope
		protected := api.Group("")
		protected.Use(middleware.Auth(r.cfg))
		protected.Use(middleware.RateLimit(r.cfg).Handler)
		protected.Use(middleware.WorkspaceScopeMiddleware())
		{
			// Use module registry for route registration
			if r.registry != nil {
				log.Info("using module registry for route registration")
				r.registry.RegisterAllRoutes(r.cfg, protected)
			} else {
				log.Warn("no module registry available, no routes registered")
			}
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
