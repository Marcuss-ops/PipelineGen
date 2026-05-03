// Package api provides HTTP API routing for the PipelineGen system.
package api

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"

	"go.uber.org/zap"

	"velox/go-master/internal/api/handlers/artlist"
	"velox/go-master/internal/api/handlers/common"
	drivehandler "velox/go-master/internal/api/handlers/drive"
	"velox/go-master/internal/api/handlers/jobs"
	imghandler "velox/go-master/internal/api/handlers/images"
	mediahandler "velox/go-master/internal/api/handlers/media"
	scraperhandler "velox/go-master/internal/api/handlers/scraper"
	"velox/go-master/internal/api/handlers/script/handlers"
	"velox/go-master/internal/api/handlers/voiceover"
	youtubecliphandler "velox/go-master/internal/api/handlers/youtubeclip"
	"velox/go-master/internal/api/middleware"
	"velox/go-master/pkg/config"
)

// Handlers holds all pre-constructed HTTP handlers
type Handlers struct {
	Health         *common.HealthHandler
	Artlist        *artlist.Handler
	Scraper        *scraperhandler.Handler
	ImageAssets    *imghandler.Handler
	Media          *mediahandler.Handler
	ScriptDocs     *handlers.ScriptDocsHandler
	ScriptHistory  *handlers.ScriptHistoryHandler
	Voiceover      *voiceover.Handler
	VoiceoverSync  *voiceover.SyncHandler
	Utility        *common.UtilityHandler
	Catalog        *common.CatalogHandler
	YouTubeClip    *youtubecliphandler.Handler
	Jobs           *jobs.Handler
	Drive          *drivehandler.Handler
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
var setupOnce sync.Once
var setupCount int

func (r *Router) Setup() *gin.Engine {
	setupCount++
	zap.L().Info("Setup() called", zap.Int("count", setupCount))
	if setupCount > 1 {
		zap.L().Panic("Setup() called multiple times!")
	}
	log := zap.L().Named("router")
	gin.SetMode(r.cfg.Server.GinMode)

	engine := gin.New()

	// Global middleware
	engine.Use(middleware.Logger())
	engine.Use(middleware.Recovery())
	engine.Use(gzip.Gzip(gzip.DefaultCompression))
	engine.Use(cors.New(buildCORSConfig(r.cfg)))

	h := r.handlers

	// Create middleware instances
	authMW := middleware.Auth(r.cfg)
	rateLimitMW := middleware.RateLimit()
	r.rateLimitMiddleware = rateLimitMW

	// Health checks (public — no auth/rate limit)
	engine.GET("/health", h.Health.Health)
	engine.GET("/api/health", h.Health.Health)

	// Serve static assets (images, etc.)
	assetsDir := filepath.Join(r.cfg.Storage.DataDir, "assets")
	engine.Static("/assets", assetsDir)

	// Public routes (no auth, no rate limit) — accessible from any machine

	public := engine.Group("/api")
	{
		// Internal utilities (can be accessed by scripts without auth)
		if h.Utility != nil {
			public.GET("/internal/slug", h.Utility.Slugify)
		}
		// Catalog API - public search endpoint for folders
		if h.Catalog != nil {
			public.GET("/catalog/folders", h.Catalog.SearchFolders)
		}
	}

	// API routes
	api := engine.Group("/api")
	{
		// Protected routes — Auth + RateLimit + WorkspaceScope
		protected := api.Group("")
		protected.Use(authMW)
		protected.Use(rateLimitMW.Handler)
		protected.Use(middleware.WorkspaceScopeMiddleware())
		{
			log.Info("registering API routes", zap.Bool("drive_handler_nil", h.Drive == nil))

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
			if h.Drive != nil {
				log.Info("registering drive routes")
				driveGroup := protected.Group("/drive")
				h.Drive.RegisterRoutes(driveGroup)
			} else {
				log.Warn("drive handler is nil, skipping route registration")
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
