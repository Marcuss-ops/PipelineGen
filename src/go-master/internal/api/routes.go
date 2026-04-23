// Package api provides HTTP API routing for the VeloxEditing system.
package api

import (
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"velox/go-master/internal/api/handlers/admin"
	artlistpipeline "velox/go-master/internal/api/handlers/artlist"
	"velox/go-master/internal/api/handlers/catalog"
	"velox/go-master/internal/api/handlers/clip"
	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/api/handlers/drive"
	"velox/go-master/internal/api/handlers/monitoring"
	"velox/go-master/internal/api/handlers/nlp"
	"velox/go-master/internal/api/handlers/script"
	"velox/go-master/internal/api/handlers/stock"
	"velox/go-master/internal/api/handlers/video"
	"velox/go-master/internal/api/handlers/youtube"
	"velox/go-master/internal/api/middleware"
	"velox/go-master/internal/audio/tts"
	"velox/go-master/internal/core/entities"
	internaldownload "velox/go-master/internal/download"
	"velox/go-master/internal/harvester"
	"velox/go-master/internal/ml/ollama"
	internalpipeline "velox/go-master/internal/service/pipeline"
	internalstock "velox/go-master/internal/stock"
	internalvideo "velox/go-master/internal/video"
	"velox/go-master/pkg/config"
)

// Handlers holds all pre-constructed HTTP handlers
type Handlers struct {
	Health            *common.HealthHandler
	Video             *video.VideoHandler
	YouTube           *youtube.YouTubeHandler
	Drive             *drive.DriveHandler
	Voiceover         *common.VoiceoverHandler
	NLP               *nlp.NLPHandler
	StockProject      *stock.StockProjectHandler
	StockSearch       *stock.StockSearchHandler
	StockProcess      *stock.StockProcessHandler
	Clip              *clip.ClipHandler
	ClipIndex         *clip.ClipIndexHandler
	Catalog           *catalog.CatalogHandler
	Download          *video.DownloadHandler
	Timestamp         *common.TimestampHandler
	ClipApproval      *clip.ClipApprovalHandler
	YouTubeV2         *youtube.YouTubeV2Handler
	GPUTextGen        *nlp.GPUTextGenHandler
	ScriptClips       *script.ScriptClipsHandler
	ScriptFromClips   *script.ScriptFromClipsHandler
	StockOrchestrator *stock.StockOrchestratorHandler
	ScriptDocs        *script.ScriptDocsHandler
	ScriptPipeline    *script.ScriptPipelineHandler
	ChannelMonitor    *monitoring.ChannelMonitorHandler
	ArtlistPipeline   *artlistpipeline.Handler
	Harvester         *harvester.Handler
	CatalogSQLite     *catalog.CatalogSQLiteHandler
	Utility           *common.UtilityHandler
	Admin             *admin.AdminHandler
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
	VideoProcessor  *internalvideo.Processor
	ScriptGen       *ollama.Generator
	OllamaClient    *ollama.Client
	EdgeTTS         *tts.EdgeTTS
	StockManager    *internalstock.StockManager
	EntityService   *entities.EntityService
	PipelineService *internalpipeline.VideoCreationService
	Downloader      *internaldownload.Downloader
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
		if h.Catalog != nil {
			h.Catalog.RegisterPublicRoutes(public)
		}

		// Drive read endpoints (folders, folder content)
		h.Drive.RegisterPublicRoutes(public)

		// Internal utilities (can be accessed by scripts without auth)
		public.GET("/internal/slug", h.Utility.Slugify)
	}

	// API routes
	api := engine.Group("/api")
	{
		// Protected routes — Auth + RateLimit
		protected := api.Group("")
		protected.Use(authMW)
		protected.Use(rateLimitMW.Handler)
		{
			// Video processing
			h.Video.RegisterRoutes(protected)
			// YouTube integration
			h.YouTube.RegisterRoutes(protected)
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
			if h.Catalog != nil {
				h.Catalog.RegisterRoutes(protected)
			}
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
			if h.ArtlistPipeline != nil {
				h.ArtlistPipeline.RegisterRoutes(protected)
			}
			if h.CatalogSQLite != nil {
				h.CatalogSQLite.RegisterRoutes(protected)
			}
			if h.Harvester != nil {
				harvesterGroup := protected.Group("/harvester")
				h.Harvester.RegisterRoutes(harvesterGroup)
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
