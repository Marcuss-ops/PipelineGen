// Package api provides HTTP API routing for the PipelineGen system.
package api

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/api/transport"
	mwports "github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	"github.com/Marcuss-ops/PipelineGen/web"
	"go.uber.org/zap"
)

// Router holds the API router configuration.
//
// PG-006 (June 2026): RouterConfig is now strictly transport-shaped —
// no concrete `*config.Config`. Auth/Rate/Features flow through the
// application-layer typed ports defined in
// internal/application/middleware/ports.go. The composition root
// (internal/app/wire_services.go and the bootstrap adapters in
// internal/app/middleware_security_adapter.go) constructs and passes
// these ports. Runtime fields the router NEEDS at request time are
// exposed as primitive typed fields (ServerGinMode, DataDir,
// DownloadDir, CORSOrigins) so the API layer stays free of
// internal/platform/config imports.
//
// QDRANT-001 (June 2026) closure: internalMediaHandler is the narrow
// port for /internal/v1/media/* routes (server-to-server surface) —
// the production binding is *internal/api/assets/storage.Handler
// (RegisterInternalMediaRoutes method). Keeping it behind an
// interface prevents this router from importing api/assets/storage.
//
// QDRANT-002 (June 2026) closure: outboxHandler is wired on the SAME
// internalGroup at /internal/v1/outbox/* (outbox monitoring endpoints
// are server-to-server, not user-facing).
//
// QDRANT-004 (June 2026) closure: mediasearchHandler is wired on the
// SAME internalGroup at /internal/v1/media/search (mediasearch is
// server-to-server). Previously it was registered through the regular
// Registry with a path rooted at /api, which collided with the
// WorkerAuth boundary. Now both routes share the WorkerAuth guard.
type Router struct {
	cfg                  *RouterConfig
	rateLimitMiddleware  *middleware.RateLimitMiddleware
	registry             *Registry
	workerHandler        interface{ RegisterRoutes(*gin.RouterGroup) }
	internalMediaHandler MediaInternalRouter
	// QDRANT-002 + QDRANT-004 closure (June 2026):
	//   - outboxHandler: /internal/v1/outbox/{status,events} (WorkerAuth).
	//   - mediasearchHandler: /internal/v1/media/search (WorkerAuth).
	// Both are server-to-server surfaces mounted on the SAME internalGroup
	// (see Setup below) — anti-regression test internal/api/routes_test.go
	// enforces the split. Typed ports isolate each handler's contract.
	outboxHandler      InternalOutboxRouter
	mediasearchHandler InternalMediaSearchRouter
	ctx                context.Context
	healthSvc          any // *systemhealth.Service; any keeps the router infra-clean.
	readyChecker       *systemhealth.ReadyChecker
	qdrantHealth       any                      // *transport.QdrantHealthHandler; any keeps the router infra-clean.
	modelsHandler      *transport.ModelsHandler // Task 10: /models endpoint (E5 + SigLIP model probes).
}

// MediaInternalRouter is the narrow port for /internal/v1/media/*
// routes. Production bind: *internal/api/assets/storage.Handler.
type MediaInternalRouter interface {
	RegisterInternalMediaRoutes(*gin.RouterGroup)
}

// InternalOutboxRouter is the narrow port for /internal/v1/outbox/*
// monitoring routes (QDRANT-002). Production bind:
// *internal/api/outbox.Handler.
type InternalOutboxRouter interface {
	RegisterRoutes(*gin.RouterGroup)
}

// InternalMediaSearchRouter is the narrow port for
// /internal/v1/media/search (QDRANT-004). Production bind:
// *internal/api/mediasearch.Handler.
type InternalMediaSearchRouter interface {
	RegisterRoutes(*gin.RouterGroup)
}

// RouterConfig is the typed-port + primitive bundle the api.Router
// proxies to middleware constructors and the static-asset router.
//
// PG-006 (June 2026): every field is either a typed application port
// or a primitive (string / []string / *zap.Logger). The composition
// root (internal/app/wire_services.go) constructs the RouterConfig
// from `*config.Config` at startup time; once handed to the api
// layer, the router does not need access to the concrete config shape.
type RouterConfig struct {
	// Typed application ports (PG-006 typed-port cascade).
	Auth     mwports.AuthSecurityPort
	Rate     mwports.RateLimitPort
	Features mwports.FeatureFlagsPort

	// Structured logger — required by Logger/Recovery/Auth/WorkerAuth
	// since these now accept *zap.Logger directly instead of going
	// through `internal/infrastructure/logging`'s package-level aliases.
	Log *zap.Logger

	// Primitive runtime fields (constructed from *config.Config by
	// the composition root before RouterConfig reaches the api layer).
	ServerGinMode string   // cfg.Server.GinMode
	DataDir       string   // cfg.Storage.DataDir
	DownloadDir   string   // cfg.GoogleAccounting.DownloadDir
	CORSOrigins   []string // cfg.Security.CORSOrigins
}

// NewRouter creates a new API router.
func NewRouter(cfg *RouterConfig) *Router {
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

// SetInternalMediaHandler wires the QDRANT-001 server-to-server media
// routes (POST /internal/v1/media/sync) into the router.
// nil-safe — if no media handler has been wired the routes simply
// won't register. Wire-up is performed by the composition root.
func (r *Router) SetInternalMediaHandler(h MediaInternalRouter) {
	r.internalMediaHandler = h
}

// SetOutboxHandler wires the QDRANT-002 server-to-server outbox
// monitoring endpoints (GET /internal/v1/outbox/status,
// GET /internal/v1/outbox/events) onto the WorkerAuth-protected
// internalGroup. nil-safe.
func (r *Router) SetOutboxHandler(h InternalOutboxRouter) {
	r.outboxHandler = h
}

// SetMediasearchHandler wires the QDRANT-004 server-to-server
// media search endpoint (POST /internal/v1/media/search) onto the
// WorkerAuth-protected internalGroup. nil-safe.
func (r *Router) SetMediasearchHandler(h InternalMediaSearchRouter) {
	r.mediasearchHandler = h
}

// SetContext sets the context for module lifecycle management
func (r *Router) SetContext(ctx context.Context) {
	r.ctx = ctx
}

// SetHealthService wires the application-layer health.Service into the router.
// The concrete type is *systemhealth.Service but the field is any
// so this file stays free of infrastructure imports (PR1 Health boundary, June 2026).
func (r *Router) SetHealthService(svc any) {
	r.healthSvc = svc
}

// SetReadyChecker wires the application-layer ReadyChecker into the router.
// codex/health-ready-contract (June 2026): previously ReadyChecker was silently
// nil in Setup(), making /ready always return 503.
func (r *Router) SetReadyChecker(rc *systemhealth.ReadyChecker) {
	r.readyChecker = rc
}

// SetQdrantHealthHandler wires the QdrantHealthHandler for
// /qdrant/live and /qdrant/ready (HIGH #7, July 2026).
// nil-safe: if not wired the routes simply won't register.
func (r *Router) SetQdrantHealthHandler(h any) {
	r.qdrantHealth = h
}

// SetModelsHandler wires the ModelsHandler for /models (Task 10, July 2026).
// nil-safe: if not wired the route returns 503.
func (r *Router) SetModelsHandler(h *transport.ModelsHandler) {
	r.modelsHandler = h
}

// buildCORSConfig builds a CORS configuration from the supplied origins.
// PG-006 (June 2026): now takes origins directly instead of a
// *config.Config — composition root extracts cfg.Security.CORSOrigins
// and hands the slice to the api layer.
func buildCORSConfig(corsOrigins []string) cors.Config {
	corsCfg := cors.Config{
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Velox-Admin-Token", "Idempotency-Key", "X-Request-ID"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: false,
		MaxAge:           12 * time.Hour,
	}

	// Require explicit CORS origins - default closed
	if len(corsOrigins) == 0 {
		corsCfg.AllowOrigins = []string{}
		return corsCfg
	}

	if len(corsOrigins) == 1 && corsOrigins[0] == "*" {
		corsCfg.AllowAllOrigins = true
		return corsCfg
	}

	corsCfg.AllowOrigins = corsOrigins
	return corsCfg
}

// Setup configures and returns the gin engine with all middleware, static routes,
// health endpoints, and dynamically registered module routes.
func (r *Router) Setup() *gin.Engine {
	log := zap.L().Named("router")
	gin.SetMode(r.cfg.ServerGinMode)

	engine := gin.New()

	// Global middleware
	engine.Use(middleware.RequestID())
	engine.Use(middleware.Logger(r.cfg.Log))
	engine.Use(middleware.Recovery(r.cfg.Log))
	engine.Use(gzip.Gzip(gzip.DefaultCompression))

	// Root redirect to health
	engine.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/health")
	})

	// Serve admin UI static files on /admin. The SPA itself is public so
	// that the browser can load the login page; the API routes remain
	// protected by RequireAdminToken / Auth. The static assets are
	// embedded at build time via web.DistFS().
	adminUIFS := web.DistFS()
	adminUIGroup := engine.Group("/admin")
	{
		adminUIGroup.StaticFS("/", http.FS(adminUIFS))
	}
	engine.NoRoute(func(c *gin.Context) {
		// RouterGroup has no NoRoute hook in Gin. Serve the SPA fallback
		// for any unknown path under /admin so react-router can handle
		// client-side routing.
		if strings.HasPrefix(c.Request.URL.Path, "/admin/") || c.Request.URL.Path == "/admin" {
			serveAdminUISPA(c, adminUIFS)
			return
		}
		c.Status(http.StatusNotFound)
	})

	registerVLMRoutes(engine)

	// Only add CORS middleware if origins are configured
	corsConfig := buildCORSConfig(r.cfg.CORSOrigins)
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
	var healthHandler *transport.HealthHandler
	if r.healthSvc != nil {
		if svc, svcOk := r.healthSvc.(*systemhealth.Service); svcOk {
			healthHandler = transport.NewHealthHandler(svc, r.readyChecker)
		}
	}
	if healthHandler == nil {
		log.Warn("health service not wired, health endpoints will return 503")
		healthHandler = transport.NewHealthHandler(nil, nil /* nil-by-design; integration stub only */)
	}
	engine.GET("/health", healthHandler.Health)
	engine.GET("/ready", healthHandler.Ready)

	// /models — E5 + SigLIP model health probes (Task 10, July 2026).

	// /models — E5 + SigLIP model health probes (Task 10, July 2026).
	// nil-safe: returns 503 when the handler is not wired.
	modelsHandler := r.modelsHandler
	if modelsHandler == nil {
		log.Warn("models handler not wired, /models will return 503")
		modelsHandler = transport.NewModelsHandler("") // empty URL -> 503 responses
	}
	engine.GET("/models", modelsHandler.Models)

	// Qdrant health endpoints — /qdrant/live (liveness) and
	// /qdrant/ready (deep readiness with alias + collection + schema
	// + semantic canary). HIGH #7, July 2026.
	if r.qdrantHealth != nil {
		if qh, ok := r.qdrantHealth.(interface {
			Live(*gin.Context)
			Ready(*gin.Context)
		}); ok {
			engine.GET("/qdrant/live", qh.Live)
			engine.GET("/qdrant/ready", qh.Ready)
		} else {
			log.Warn("qdrantHealth handler does not satisfy Live/Ready interface, routes not registered")
		}
	}

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
	assetsDir := filepath.Join(r.cfg.DataDir, "assets")
	engine.Static("/assets", assetsDir)
	engine.Static("/media/google-accounting", r.cfg.DownloadDir)

	// API routes
	api := engine.Group("/api")
	{
		// Admin authentication surface for the React SPA. Login/logout are
		// intentionally public; /me is protected by RequireAdminToken so the
		// frontend can verify its session cookie.
		adminAuth := api.Group("/admin/auth")
		{
			secureCookie := r.cfg.ServerGinMode == gin.ReleaseMode

			adminAuth.POST("/login", func(c *gin.Context) {
				var req struct {
					Token string `json:"token"`
				}
				if err := c.ShouldBindJSON(&req); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid request body"})
					return
				}
				if r.cfg.Auth == nil {
					c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "auth not configured"})
					return
				}
				if !middleware.CompareTokens(req.Token, r.cfg.Auth.AdminToken()) {
					c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "invalid token"})
					return
				}
				c.SetSameSite(http.SameSiteLaxMode)
				c.SetCookie(
					"velox_admin_session",
					req.Token,
					86400,
					"/",
					"",
					secureCookie,
					true,
				)
				c.JSON(http.StatusOK, gin.H{"ok": true})
			})

			adminAuth.POST("/logout", func(c *gin.Context) {
				c.SetSameSite(http.SameSiteLaxMode)
				c.SetCookie(
					"velox_admin_session",
					"",
					-1,
					"/",
					"",
					secureCookie,
					true,
				)
				c.JSON(http.StatusOK, gin.H{"ok": true})
			})

			adminAuth.GET("/me", middleware.RequireAdminToken(r.cfg.Auth, r.cfg.Log), func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"ok": true, "role": "admin"})
			})
		}

		// Protected routes — Auth + RateLimit + WorkspaceScope
		protected := api.Group("")
		protected.Use(middleware.Auth(r.cfg.Auth, r.cfg.Log))
		r.rateLimitMiddleware = middleware.RateLimit(r.cfg.Rate)
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

	// QDRANT-002 + QDRANT-004 (June 2026): the internal-worker-broker
	// prefix is "/internal/v1" — historically `remoteshared.InternalPathPrefix`.
	// The Wave 14 PR5 cleanup hardcodes it here so internal/api stops
	// importing internal/infrastructure/remote/shared (a transport concern,
	// not a capability concern). Anti-regression test
	// internal/api/routes_test.go::TestRoutes_NoApiInternalV1Prefix enforces
	// no /api/internal/v1/* route should ever leak.
	internalGroup := engine.Group("/internal/v1")
	internalGroup.Use(middleware.WorkerAuth(r.cfg.Auth, r.cfg.Log))
	{
		if r.workerHandler != nil {
			r.workerHandler.RegisterRoutes(internalGroup)
		}
		// QDRANT-001 /internal/v1/media/* surface — server-to-server.
		// WorkerAuth above enforces Bearer token (rejects admin tokens —
		// see middleware_worker_auth_test.go). nil-tolerant if not wired.
		if r.internalMediaHandler != nil {
			r.internalMediaHandler.RegisterInternalMediaRoutes(internalGroup)
		}
		// QDRANT-002 /internal/v1/outbox/* surface — server-to-server
		// outbox monitoring (GET /status, GET /events). Mounted on the
		// SAME WorkerAuth internalGroup as worker routes; anti-regression
		// test TestRoutes_NoApiInternalV1Prefix forbids ever moving this
		// under /api.
		if r.outboxHandler != nil {
			outboxGroup := internalGroup.Group("/outbox")
			r.outboxHandler.RegisterRoutes(outboxGroup)
		}
		// QDRANT-004 /internal/v1/media/search — server-to-server
		// semantic search. Mounted on the SAME WorkerAuth internalGroup.
		if r.mediasearchHandler != nil {
			mediaSearchGroup := internalGroup.Group("/media")
			r.mediasearchHandler.RegisterRoutes(mediaSearchGroup)
		}
	}

	// Log all registered routes
	for _, route := range engine.Routes() {
		log.Info("registered route", zap.String("method", route.Method), zap.String("path", route.Path))
	}

	// Build the WireRegistry AFTER all routes are registered and wire
	// it into the /ready handler. This is the canonical SSOT site for
	// the wire surface — earlier SetWireRegistry calls would observe
	// only the routes registered up to that point (the original bug:
	// an early call saw no /api/* routes and reported all
	// capabilities NOT_MOUNTED).
	//
	// The wire field lets operators detect 404'd capabilities (e.g.
	// `wire: stock: NOT_MOUNTED`) without grepping server logs.
	// Captured the 2026-07-07 stale-binary incident: the new
	// pipelinegen binary lost stock-pipeline mount, /api/stock-pipeline/run
	// returned 400 (validation) so the bug wasn't visible — a /ready
	// "wire: stock: NOT_MOUNTED" would have caught it in 5 seconds.
	//
	// Also sync the wire_capability_mounted Prometheus gauge so
	// Grafana can alert on capability drift without polling /ready.
	wireReg := transport.NewWireRegistryFromEngine(engine)
	healthHandler.SetWireRegistry(wireReg)
	transport.SyncWireCapabilityMounted(wireReg)

	// Wire the script.generate route-mounted probe into the readiness
	// checker so the script_generate check can fail closed when the
	// /api/script capability is not mounted.
	if r.readyChecker != nil {
		r.readyChecker.SetScriptRouteMounted(func() bool { return wireReg.IsMounted("script") })
	}

	// GET /api/capabilities — expose mounted capabilities and version.
	// Mounted after the wire registry is built so it reflects the
	// actual routed surface.
	api.GET("/capabilities", transport.NewCapabilitiesHandler(wireReg, "", "v2").Capabilities)

	return engine
}

// serveAdminUISPA serves the embedded index.html for SPA fallback.
func serveAdminUISPA(c *gin.Context, fsys fs.FS) {
	file, err := fsys.Open("index.html")
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	data, err := io.ReadAll(file)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	// http.ServeContent sets Content-Type, handles Range requests,
	// and respects If-Modified-Since headers. fs.File is not guaranteed
	// to implement io.ReadSeeker, so serve an in-memory reader.
	http.ServeContent(c.Writer, c.Request, "index.html", stat.ModTime(), bytes.NewReader(data))
}

// Stop cleans up resources used by the router (rate limiter goroutines)
func (r *Router) Stop() {
	if r.rateLimitMiddleware != nil {
		r.rateLimitMiddleware.Stop()
	}
}
