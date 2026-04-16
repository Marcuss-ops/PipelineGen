// Package api provides HTTP API routing for the VeloxEditing system.
package api

import (
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"velox/go-master/internal/api/handlers"
	artlistpipeline "velox/go-master/internal/api/handlers/artlist"
	"velox/go-master/internal/api/middleware"
	"velox/go-master/internal/audio/tts"
	"velox/go-master/internal/core/entities"
	"velox/go-master/internal/download"
	"velox/go-master/internal/harvester"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/service/pipeline"
	"velox/go-master/internal/stock"
	"velox/go-master/internal/video"
	"velox/go-master/pkg/config"
)

// Handlers holds all pre-constructed HTTP handlers
type Handlers struct {
	Job               *handlers.JobHandler
	Worker            *handlers.WorkerHandler
	Health            *handlers.HealthHandler
	Admin             *handlers.AdminHandler
	Video             *handlers.VideoHandler
	YouTube           *handlers.YouTubeHandler
	Script            *handlers.ScriptHandler
	Drive             *handlers.DriveHandler
	Voiceover         *handlers.VoiceoverHandler
	NLP               *handlers.NLPHandler
	StockProject      *handlers.StockProjectHandler
	StockSearch       *handlers.StockSearchHandler
	StockProcess      *handlers.StockProcessHandler
	Clip              *handlers.ClipHandler
	ClipIndex         *handlers.ClipIndexHandler
	Dashboard         *handlers.DashboardHandler
	Stats             *handlers.StatsHandler
	Scraper           *handlers.ScraperHandler
	Download          *handlers.DownloadHandler
	Timestamp         *handlers.TimestampHandler
	ClipApproval      *handlers.ClipApprovalHandler
	YouTubeV2         *handlers.YouTubeV2Handler
	GPUTextGen        *handlers.GPUTextGenHandler
	ScriptClips       *handlers.ScriptClipsHandler
	ScriptFromClips   *handlers.ScriptFromClipsHandler
	StockOrchestrator *handlers.StockOrchestratorHandler
	ScriptDocs        *handlers.ScriptDocsHandler
	ScriptPipeline    *handlers.ScriptPipelineHandler
	ChannelMonitor    *handlers.ChannelMonitorHandler
	AsyncPipeline     *handlers.AsyncPipelineHandler
	ArtlistPipeline   *artlistpipeline.Handler
	Harvester         *harvester.Handler
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
	VideoProcessor  *video.Processor
	ScriptGen       *ollama.Generator
	OllamaClient    *ollama.Client
	EdgeTTS         *tts.EdgeTTS
	StockManager    *stock.StockManager
	EntityService   *entities.EntityService
	PipelineService *pipeline.VideoCreationService
	Downloader      *download.Downloader
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

		// Clip read endpoints (suggest, search, list, folder browsing)
		h.Clip.RegisterPublicRoutes(public)
		h.ClipIndex.RegisterPublicRoutes(public)

		// Drive read endpoints (folders, folder content)
		h.Drive.RegisterPublicRoutes(public)
	}

	// API routes
	api := engine.Group("/api")
	{
		// Protected routes — Auth + RateLimit
		protected := api.Group("")
		protected.Use(authMW)
		protected.Use(rateLimitMW.Handler)
		{
			// Job management
			h.Job.RegisterRoutes(protected)
			// Worker management
			h.Worker.RegisterRoutes(protected)
			// Video processing
			h.Video.RegisterRoutes(protected)
			// YouTube integration
			h.YouTube.RegisterRoutes(protected)
			// Script generation
			h.Script.RegisterRoutes(protected)
			// Google Drive (write operations)
			h.Drive.RegisterRoutes(protected)
			// Voiceover
			h.Voiceover.RegisterRoutes(protected)
			// NLP
			h.NLP.RegisterRoutes(protected)
			// Stock - Projects
			h.StockProject.RegisterRoutes(protected)
			// Stock - Search
			h.StockSearch.RegisterRoutes(protected)
			// Stock - Process
			h.StockProcess.RegisterRoutes(protected)
			// Clip (write operations)
			h.Clip.RegisterRoutes(protected)
			// Clip Index (write operations)
			h.ClipIndex.RegisterRoutes(protected)
			// Dashboard
			h.Dashboard.RegisterRoutes(protected)
			// Stats
			h.Stats.RegisterRoutes(protected)
			// Scraper
			h.Scraper.RegisterRoutes(protected)
			// Admin
			h.Admin.RegisterRoutes(protected)
			// Download
			h.Download.RegisterRoutes(protected)

			if h.Timestamp != nil {
				h.Timestamp.RegisterRoutes(protected)
			}
			if h.ClipApproval != nil {
				h.ClipApproval.RegisterRoutes(protected)
			}
			if h.YouTubeV2 != nil {
				h.YouTubeV2.RegisterRoutes(protected)
			}
			if h.GPUTextGen != nil {
				h.GPUTextGen.RegisterRoutes(protected)
			}
			if h.ScriptClips != nil {
				h.ScriptClips.RegisterRoutes(protected)
			}
			if h.ScriptFromClips != nil {
				h.ScriptFromClips.RegisterRoutes(protected)
			}
			if h.StockOrchestrator != nil {
				h.StockOrchestrator.RegisterRoutes(protected)
			}
			if h.ScriptDocs != nil {
				scriptDocsGroup := protected.Group("/script-docs")
				h.ScriptDocs.RegisterRoutes(scriptDocsGroup)
			}
			if h.ChannelMonitor != nil {
				h.ChannelMonitor.RegisterRoutes(protected)
			}
			if h.AsyncPipeline != nil {
				h.AsyncPipeline.RegisterRoutes(protected)
			}
			if h.ArtlistPipeline != nil {
				h.ArtlistPipeline.RegisterRoutes(protected)
			}
			if h.Harvester != nil {
				h.Harvester.RegisterRoutes(protected)
			}
			if h.ScriptPipeline != nil {
				h.ScriptPipeline.RegisterRoutes(protected)
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
