// Package api provides HTTP API routing for the PipelineGen system.
package api

import (
	"context"
	"path/filepath"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"

	"go.uber.org/zap"

	artlistHandler "velox/go-master/internal/api/handlers/artlist"
	"velox/go-master/internal/api/handlers/common"
	drivehandler "velox/go-master/internal/api/handlers/drive"
	imghandler "velox/go-master/internal/api/handlers/images"
	"velox/go-master/internal/api/handlers/jobs"
	mediahandler "velox/go-master/internal/api/handlers/media"
	scraperhandler "velox/go-master/internal/api/handlers/scraper"
	"velox/go-master/internal/api/handlers/script/handlers"
	"velox/go-master/internal/api/handlers/voiceover"
	workflowhandler "velox/go-master/internal/api/handlers/workflow"
	youtubecliphandler "velox/go-master/internal/api/handlers/youtubeclip"
	"velox/go-master/internal/api/middleware"
	"velox/go-master/internal/module"
	"velox/go-master/pkg/config"
)

// Handlers holds all pre-constructed HTTP handlers
type Handlers struct {
	Health        *common.HealthHandler
	Artlist       *artlistHandler.Handler
	Scraper       *scraperhandler.Handler
	ImageAssets   *imghandler.Handler
	Media         *mediahandler.CommonHandler
	ScriptDocs    *handlers.ScriptDocsHandler
	ScriptHistory *handlers.ScriptHistoryHandler
	Voiceover     *voiceover.Handler
	VoiceoverSync *voiceover.SyncHandler
	Utility       *common.UtilityHandler
	Catalog       *common.CatalogHandler
	YouTubeClip   *youtubecliphandler.Handler
	Jobs          *jobs.Handler
	Drive         *drivehandler.Handler
	Workflow      *workflowhandler.Handler
}

// Router holds all API handlers
type Router struct {
	cfg                 *config.Config
	handlers            *Handlers
	rateLimitMiddleware *middleware.RateLimitMiddleware
	registry            *module.Registry
	ctx                 context.Context
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
	engine.Use(middleware.Logger())
	engine.Use(middleware.Recovery())
	engine.Use(gzip.Gzip(gzip.DefaultCompression))
	
	// Only add CORS middleware if origins are configured
	corsConfig := buildCORSConfig(r.cfg)
	if len(corsConfig.AllowOrigins) > 0 || corsConfig.AllowAllOrigins {
		engine.Use(cors.New(corsConfig))
	} else {
		log.Info("CORS disabled - no origins configured")
	}

	// Health checks (public — no auth/rate limit)
	engine.GET("/health", r.handlers.Health.Health)
	engine.GET("/api/health", r.handlers.Health.Health)

	// Serve static assets (images, etc.)
	assetsDir := filepath.Join(r.cfg.Storage.DataDir, "assets")
	engine.Static("/assets", assetsDir)

// API routes
api := engine.Group("/api")
{
	// Public catalog route (no auth required)
	if r.handlers.Catalog != nil {
		api.GET("/catalog/folders", r.handlers.Catalog.SearchFolders)
	}

	// Protected routes — Auth + RateLimit + WorkspaceScope
	protected := api.Group("")
		protected.Use(middleware.Auth(r.cfg))
		protected.Use(middleware.RateLimit().Handler)
		protected.Use(middleware.WorkspaceScopeMiddleware())
		{
			// Use module registry if available, otherwise fall back to handler registration
			if r.registry != nil {
				log.Info("using module registry for route registration")
				r.registry.RegisterAllRoutes(r.cfg, protected)
			} else {
				log.Info("using legacy handler registration")
				r.registerHandlersLegacy(protected)
			}
		}
	}

	// Log all registered routes
	for _, route := range engine.Routes() {
		log.Info("registered route", zap.String("method", route.Method), zap.String("path", route.Path))
	}

	return engine
}

// registerHandlersLegacy registers routes using the old handler-based approach
func (r *Router) registerHandlersLegacy(protected *gin.RouterGroup) {
	log := zap.L().Named("router")
	h := r.handlers

	log.Info("registering API routes", zap.Bool("drive_handler_nil", h.Drive == nil))

	// Internal utilities (now protected)
	if h.Utility != nil {
		protected.GET("/internal/slug", h.Utility.Slugify)
	}

	if h.Drive != nil {
		log.Info("registering drive routes")
		driveGroup := protected.Group("/drive")
		h.Drive.RegisterRoutes(driveGroup)
	} else {
		log.Warn("drive handler is nil, skipping route registration")
	}

	if h.Artlist != nil {
		artlistGroup := protected.Group("/artlist")
		artlistGroup.Use(middleware.ArtlistEnabled(r.cfg))
		h.Artlist.RegisterRoutes(artlistGroup)
	}
	if h.ImageAssets != nil {
		imgGroup := protected.Group("/images")
		h.ImageAssets.RegisterRoutes(imgGroup)
	}
	if h.Scraper != nil {
		scraperGroup := protected.Group("/scraper")
		h.Scraper.RegisterRoutes(scraperGroup)
	}
	if h.ScriptDocs != nil {
		scriptDocsGroup := protected.Group("/script-docs")
		scriptDocsGroup.Use(middleware.ScriptDocsEnabled(r.cfg))
		h.ScriptDocs.RegisterRoutes(scriptDocsGroup)
	}
	if h.ScriptHistory != nil {
		scriptHistoryGroup := protected.Group("/scripts")
		scriptHistoryGroup.Use(middleware.ScriptClipsEnabled(r.cfg))
		h.ScriptHistory.RegisterRoutes(scriptHistoryGroup)
	}
	if h.Voiceover != nil {
		voGroup := protected.Group("/voiceover")
		h.Voiceover.RegisterRoutes(voGroup)
	}
	if h.VoiceoverSync != nil {
		voSyncGroup := protected.Group("/voiceover")
		h.VoiceoverSync.RegisterRoutes(voSyncGroup)
	}
	if h.YouTubeClip != nil {
		youtubeGroup := protected.Group("/youtube-clips")
		youtubeGroup.Use(middleware.YouTubeEnabled(r.cfg))
		h.YouTubeClip.RegisterRoutes(youtubeGroup)
	}
	if h.Jobs != nil {
		jobsGroup := protected.Group("/jobs")
		h.Jobs.RegisterRoutes(jobsGroup)
	}
	if h.Media != nil {
		mediaGroup := protected.Group("/media")
		h.Media.RegisterRoutes(mediaGroup)
	}
	if h.Workflow != nil {
		workflowGroup := protected.Group("/workflows")
		h.Workflow.RegisterRoutes(workflowGroup)
	}
}

// Stop cleans up resources used by the router (rate limiter goroutines)
func (r *Router) Stop() {
	if r.rateLimitMiddleware != nil {
		r.rateLimitMiddleware.Stop()
	}
}
