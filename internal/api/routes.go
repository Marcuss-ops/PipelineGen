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

	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/api/transport"
	mwports "github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
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
//
// The per-domain wiring lives in sibling files of this package:
//   - routes_admin.go    — /admin SPA static surface + /api/admin/auth login.
//   - routes_health.go   — /health, /ready, /models, /qdrant/* health surface.
//   - routes_api.go      — /api module-registry surface (media, jobs, ...).
//   - routes_internal.go — /internal/v1 WorkerAuth surface (worker/jobs,
//     media sync, outbox monitoring, media search).
//   - routes_metrics.go  — /metrics (PR-METRICS-FAILCLOSED).
//
// Ordering contracts that MUST survive the split:
//  1. Global middleware mounts first (RequestID → Logger → Recovery →
//     gzip); CORS is added after registerVLMRoutes.
//  2. The WireRegistry is built AFTER every route is registered, then the
//     /ready handler and the /api/capabilities route receive it —
//     /api/capabilities is intentionally mounted last so it reflects the
//     actual routed surface.
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

	// Admin SPA static surface + engine-level NoRoute SPA fallback.
	r.registerAdminUIRoutes(engine)

	registerVLMRoutes(engine)

	// Only add CORS middleware if origins are configured
	corsConfig := buildCORSConfig(r.cfg.CORSOrigins)
	if len(corsConfig.AllowOrigins) > 0 || corsConfig.AllowAllOrigins {
		engine.Use(cors.New(corsConfig))
	} else {
		log.Info("CORS disabled - no origins configured")
	}

	// Health surface (/health, /ready, /models, /qdrant/*). Returns the
	// handler so the WireRegistry can be attached once all routes mount.
	healthHandler := r.registerHealthRoutes(engine, log)

	// /metrics — fail-closed in release mode (PR-METRICS-FAILCLOSED).
	r.registerMetricsRoute(engine, log)

	// Serve static assets (images, etc.)
	assetsDir := filepath.Join(r.cfg.DataDir, "assets")
	engine.Static("/assets", assetsDir)
	engine.Static("/media/google-accounting", r.cfg.DownloadDir)

	// Public /api surface (admin auth + protected module registry).
	// Returns the group so /api/capabilities can mount AFTER the
	// WireRegistry exists.
	api := r.registerAPIRoutes(engine, log)

	// WorkerAuth-protected /internal/v1 surface (worker, media, outbox).
	r.registerInternalRoutes(engine)

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

// Stop cleans up resources used by the router (rate limiter goroutines)
func (r *Router) Stop() {
	if r.rateLimitMiddleware != nil {
		r.rateLimitMiddleware.Stop()
	}
}
