// Package api provides HTTP API routing for the VeloxEditing system.
package api

import (
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/api/handlers/drive"
	"velox/go-master/internal/api/handlers/script"
	"velox/go-master/internal/api/middleware"
	"velox/go-master/internal/core/entities"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/pkg/config"
)

// Handlers holds all pre-constructed HTTP handlers
type Handlers struct {
	Health     *common.HealthHandler
	Drive      *drive.DriveHandler
	ScriptDocs *script.ScriptDocsHandler
	Utility    *common.UtilityHandler
}

// RouterDepsWithHandlers holds both handlers and raw dependencies
type RouterDepsWithHandlers struct {
	Handlers *Handlers
	Deps     *RouterDeps
}

// Router holds all API handlers
type Router struct {
	cfg                 *config.Config
	handlers            *Handlers
	rateLimitMiddleware *middleware.RateLimitMiddleware
}

// RouterDeps contains all external dependencies for the router
type RouterDeps struct {
	ScriptGen     *ollama.Generator
	OllamaClient  *ollama.Client
	EntityService *entities.EntityService
}

// NewRouter creates a new API router with pre-constructed handlers
func NewRouter(
	cfg *config.Config,
	h *Handlers,
) *Router {
	return &Router{
		cfg:      cfg,
		handlers: h,
	}
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
	if len(origins) == 0 {
		corsCfg.AllowAllOrigins = true
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
	gin.SetMode(r.cfg.Server.GinMode)

	engine := gin.New()

	// Global middleware
	engine.Use(middleware.Logger())
	engine.Use(middleware.Recovery())
	engine.Use(gzip.Gzip(gzip.DefaultCompression))
	engine.Use(cors.New(buildCORSConfig(r.cfg)))

	h := r.handlers

	// Create middleware instances
	authMW := middleware.Auth()
	rateLimitMW := middleware.RateLimit()
	r.rateLimitMiddleware = rateLimitMW

	// Health checks (public — no auth/rate limit)
	engine.GET("/health", h.Health.Health)
	engine.GET("/api/health", h.Health.Health)

	// Public routes (no auth, no rate limit) — accessible from any machine
	public := engine.Group("/api")
	{
		// Swagger docs
		public.GET("/docs/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

		// Drive read endpoints (folders, folder content)
		if h.Drive != nil {
			h.Drive.RegisterPublicRoutes(public)
		}

		// Internal utilities (can be accessed by scripts without auth)
		if h.Utility != nil {
			public.GET("/internal/slug", h.Utility.Slugify)
		}
	}

	// API routes
	api := engine.Group("/api")
	{
		// Protected routes — Auth + RateLimit
		protected := api.Group("")
		protected.Use(authMW)
		protected.Use(rateLimitMW.Handler)
		{
			if h.Drive != nil {
				h.Drive.RegisterRoutes(protected)
			}
			if h.ScriptDocs != nil {
				scriptDocsGroup := protected.Group("/script-docs")
				h.ScriptDocs.RegisterRoutes(scriptDocsGroup)
			}
		}
	}

	return engine
}

// Stop cleans up resources used by the router (rate limiter goroutines)
func (r *Router) Stop() {
	if r.rateLimitMiddleware != nil {
		r.rateLimitMiddleware.Stop()
	}
}
