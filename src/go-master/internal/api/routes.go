// Package api provides HTTP API routing for the VeloxEditing system.
package api

import (
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"

	"velox/go-master/internal/api/handlers/artlist"
	"velox/go-master/internal/api/handlers/common"
	scraperhandler "velox/go-master/internal/api/handlers/scraper"
	"velox/go-master/internal/api/handlers/script/handlers"
	"velox/go-master/internal/api/handlers/voiceover"
	"velox/go-master/internal/api/middleware"
	"velox/go-master/pkg/config"
)

// Handlers holds all pre-constructed HTTP handlers
type Handlers struct {
	Health        *common.HealthHandler
	Artlist       *artlist.Handler
	Scraper       *scraperhandler.Handler
	ScriptDocs    *handlers.ScriptDocsHandler
	ScriptHistory *handlers.ScriptHistoryHandler
	Voiceover     *voiceover.Handler
	Utility       *common.UtilityHandler
}

// Router holds all API handlers
type Router struct {
	cfg                 *config.Config
	handlers            *Handlers
	rateLimitMiddleware *middleware.RateLimitMiddleware
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
			if h.Artlist != nil {
				artlistGroup := protected.Group("/artlist")
				h.Artlist.RegisterRoutes(artlistGroup)
			}
			if h.Scraper != nil {
				scraperGroup := protected.Group("/scraper")
				h.Scraper.RegisterRoutes(scraperGroup)
			}
			if h.ScriptDocs != nil {
				scriptDocsGroup := protected.Group("/script-docs")
				h.ScriptDocs.RegisterRoutes(scriptDocsGroup)
			}
			if h.ScriptHistory != nil {
				scriptHistoryGroup := protected.Group("/scripts")
				h.ScriptHistory.RegisterRoutes(scriptHistoryGroup)
			}
			if h.Voiceover != nil {
				voGroup := protected.Group("/voiceover")
				h.Voiceover.RegisterRoutes(voGroup)
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
