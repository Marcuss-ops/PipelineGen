// Package api provides the HTTP server for the PipelineGen system.
package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/transport"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/middleware"
)

// LifecycleManager is the minimal lifecycle contract the server needs.
// The composition root (internal/app) implements this to manage background
// services (job runner, dispatchers, channel monitors, etc.).
//
// QDRANT-005 (June 2026) closure: the interface now exposes AddProbe so
// the readiness barrier can be EXTENDED at runtime by callers that
// wire dependencies after the lifecycle is constructed (e.g. the
// Qdrant probe in cmd/server/main.go: now done via AddProbe instead of
// silently failing through a type-assertion any downsizing).
// Implementations MUST accept AddProbe calls BEFORE Start runs.
type LifecycleManager interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	// AddProbe registers an additional readiness probe. name is used
	// for logging only; duplicate-name semantics are implementation
	// defined (current impl: append, no dedup).
	AddProbe(name string, probe func(ctx context.Context) error)
}

// Server represents the HTTP server.
// Background services (maintenance, watchers, etc.) are managed externally
// by a LifecycleManager — not by the Server.
type Server struct {
	cfg                 *config.Config
	router              *gin.Engine
	appRouter           *Router // reference to the Router for cleanup
	httpServer          *http.Server
	lifecycle           LifecycleManager
	imageSearchResolver any // FASE 7 singleton (server-side)
}

// NewServer creates a new HTTP server with module registry support.
// workerHandler (optional) is wired into /internal/v1 routes.
// lifecycle (optional) is used for Start/Stop of background services.
// healthSvc (optional) is the application-layer health.Service; when nil,
// health endpoints return 503.
func NewServer(
	cfg *config.Config,
	registry *Registry,
	workerHandler interface{ RegisterRoutes(*gin.RouterGroup) },
	internalMediaHandler MediaInternalRouter,
	lifecycle LifecycleManager,
) *Server {
	return NewServerWithHealth(ServerDeps{
		Config:    cfg,
		Registry:  registry,
		Handlers:  InternalHandlers{Worker: workerHandler, Media: internalMediaHandler},
		Lifecycle: lifecycle,
	})
}

// InternalHandlers bundles the optional typed-port internal-route handlers.
// MUST be wired before router.Setup() runs — the QDRANT-route-constructor
// bug (June 2026) proved that post-Setup setters silently drop routes.
// All 4 handlers are nil-friendly; zero-valued fields are skipped.
type InternalHandlers struct {
	Worker      interface{ RegisterRoutes(*gin.RouterGroup) }
	Media       MediaInternalRouter
	Outbox      InternalOutboxRouter
	MediaSearch InternalMediaSearchRouter
}

// ServerDeps groups constructor dependencies by real capability.
// Replaces the 9-param flat constructor with 3 grouped bundles.
type ServerDeps struct {
	Config    *config.Config
	Registry  *Registry
	Handlers  InternalHandlers
	Lifecycle LifecycleManager
	Health    any
	Ready     any
	// QdrantHealth is the HIGH #7 handler for /qdrant/live and /qdrant/ready.
	// Concrete type: *transport.QdrantHealthHandler; nil-safe when Qdrant is disabled.
	QdrantHealth any
	// ModelsSidecarURL is the Python embedding server URL for the /models endpoint
	// (Task 10, July 2026). When empty, /models returns "sidecar not configured".
	// Default: ClipIndexer.ServerURL (typically http://127.0.0.1:8001).
	ModelsSidecarURL string
	// ImageSearchResolver (FASE 7, July 2026): the canonical routing
	// singleton reached from app.DomainBundle.ImageSearchResolver.
	ImageSearchResolver any
}

// NewServerWithHealth creates a new HTTP server from grouped dependency bundles.
func NewServerWithHealth(deps ServerDeps) *Server {
	cfg := deps.Config
	registry := deps.Registry
	workerHandler := deps.Handlers.Worker
	internalMediaHandler := deps.Handlers.Media
	outboxHandler := deps.Handlers.Outbox
	mediasearchHandler := deps.Handlers.MediaSearch
	lifecycle := deps.Lifecycle
	healthSvc := deps.Health
	readyChecker := deps.Ready
	if cfg != nil {
		// PG-006.1 (June 2026): the inline serverSecurityAdapter was deleted
		// — the canonical concrete is
		// internal/api/middleware.TokenSecurityAdapter (re-located from
		// pkg/middleware round-2; pkg/ is leaf-only and HTTP-middleware
		// concrete adapters cannot legitimately live there). cfg.Security
		// is snapshotted into the canonical adapter literal here; the
		// adapter is immutable per-token-string once constructed. Enable
		// is the cfg.Security.EnableAuth passthrough (preserves the
		// pre-PG-006.1 serverSecurityAdapter.EnableAuth() semantics).
		authAdapter := &middleware.TokenSecurityAdapter{
			Enable: cfg.Security.EnableAuth,
			Admin:  cfg.Security.AdminToken,
			Worker: cfg.Security.WorkerToken,
		}
		rateAdapter := &serverRateLimitAdapter{cfg: cfg}
		featuresAdapter := &serverFeatureFlagsAdapter{cfg: cfg}
		router := NewRouter(&RouterConfig{
			Auth:          authAdapter,
			Rate:          rateAdapter,
			Features:      featuresAdapter,
			Log:           zap.L().Named("router"),
			ServerGinMode: cfg.Server.GinMode,
			DataDir:       cfg.Storage.DataDir,
			DownloadDir:   cfg.GoogleAccounting.DownloadDir,
			CORSOrigins:   cfg.Security.CORSOrigins,
		})
		router.SetRegistry(registry)
		if workerHandler != nil {
			router.SetWorkerHandler(workerHandler)
		}
		if internalMediaHandler != nil {
			router.SetInternalMediaHandler(internalMediaHandler)
		}
		// QDRANT-route-constructor (June 2026, PR 3): the outbox + mediasearch
		// wiring MUST happen here, before Setup() runs. ServerDeps is the
		// sole server-level wiring path; Router receives these handlers
		// before its route table is finalized.
		if outboxHandler != nil {
			router.SetOutboxHandler(outboxHandler)
		}
		if mediasearchHandler != nil {
			router.SetMediasearchHandler(mediasearchHandler)
		}
		if healthSvc != nil {
			router.SetHealthService(healthSvc)
		}
		if readyChecker != nil {
			router.SetReadyChecker(readyChecker)
		}
		// Mirror the QdrantHealth wiring from the cfg=nil fallback
		// branch below so production cfg-shaped servers register
		// /qdrant/live and /qdrant/ready when Qdrant is enabled.
		if deps.QdrantHealth != nil {
			router.SetQdrantHealthHandler(deps.QdrantHealth)
		}
		// Task 10: /models endpoint — wire ModelsHandler from sidecar URL.
		if deps.ModelsSidecarURL != "" {
			router.SetModelsHandler(transport.NewModelsHandler(deps.ModelsSidecarURL))
		}
		r := router.Setup()

		return &Server{
			cfg:       cfg,
			router:    r,
			appRouter: router,
			lifecycle: lifecycle,
			httpServer: &http.Server{
				Addr:              fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
				Handler:           r,
				ReadTimeout:       time.Duration(cfg.Server.ReadTimeout) * time.Second,
				ReadHeaderTimeout: 15 * time.Second,
				WriteTimeout:      time.Duration(cfg.Server.WriteTimeout) * time.Second,
				IdleTimeout:       120 * time.Second,
			},
			imageSearchResolver: deps.ImageSearchResolver, // FASE 7: canonical routing singleton
		}
	}

	router := NewRouter(&RouterConfig{Log: zap.L().Named("router")})
	router.SetRegistry(registry)
	if workerHandler != nil {
		router.SetWorkerHandler(workerHandler)
	}
	if internalMediaHandler != nil {
		router.SetInternalMediaHandler(internalMediaHandler)
	}
	// QDRANT-route-constructor (June 2026, PR 3): same pre-Setup wiring as
	// the cfg != nil branch above. ServerDeps is the sole server-level
	// wiring path. Both branches register the routes
	// identically — verified by internal/api/routes_test.go::
	// TestNewServerWithHealth_ProductionShapedRoutes (uses the no-cfg
	// fallback branch because the test does not want a real *config.Config
	// fixture).
	if outboxHandler != nil {
		router.SetOutboxHandler(outboxHandler)
	}
	if mediasearchHandler != nil {
		router.SetMediasearchHandler(mediasearchHandler)
	}
	if healthSvc != nil {
		router.SetHealthService(healthSvc)
	}
	if readyChecker != nil {
		router.SetReadyChecker(readyChecker)
	}
	if deps.QdrantHealth != nil {
		router.SetQdrantHealthHandler(deps.QdrantHealth)
	}
	// Task 10: /models endpoint — wire ModelsHandler from sidecar URL.
	if deps.ModelsSidecarURL != "" {
		router.SetModelsHandler(transport.NewModelsHandler(deps.ModelsSidecarURL))
	}
	r := router.Setup()

	return &Server{
		cfg:       cfg,
		router:    r,
		appRouter: router,
		lifecycle: lifecycle,
		httpServer: &http.Server{
			Addr:         ":0",
			Handler:      r,
			ReadTimeout:  300 * time.Second,
			WriteTimeout: 300 * time.Second,
			IdleTimeout:  120 * time.Second,
		},
		imageSearchResolver: deps.ImageSearchResolver, // FASE 7: canonical routing singleton
	}
}

// SetLifecycle wires a LifecycleManager after construction (for callers
// that don't pass it through NewServer).
func (s *Server) SetLifecycle(lc LifecycleManager) {
	s.lifecycle = lc
}

// Start starts the HTTP server via an internal signal-aware context.
// Background services are managed by the LifecycleManager — this
// method only handles the HTTP lifecycle.
//
// Retained for back-compat after P2-1 (June 2026) added StartWithContext.
// New callers should use StartWithContext directly so signal-handling
// ownership stays unambiguous (cmd/server/main.go owns OS signals and
// hands the resulting ctx into the runtime; cmd/server no longer relies
// on this internal-signal path).
func (s *Server) Start() error {
	rootCtx, rootCancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer rootCancel()
	return s.StartWithContext(rootCtx)
}

// StartWithContext drives the HTTP server until ctx is cancelled.
// Caller is responsible for OS signal handling: cmd/server/main.go
// (P2-1, June 2026) wraps context.Background() in signal.NotifyContext
// and passes the resulting ctx here. The ctx is the SINGLE source of
// cancellation for the entire server lifecycle (HTTP serve loop,
// lifecycle startup, readiness-barrier provenance, graceful-shutdown
// drain).
//
// The internal signal-aware context that existed pre-P2-1 is preserved
// in Start() for any caller that doesn't yet pass a context through.
// Adding StartWithContext does NOT change Start's behaviour; both
// callers receive identical error semantics + shutdown ordering.
//
// Pattern parallels internal/app/workerruntime.Run (P1-3): main owns
// OS signals, the runtime owns the HTTP+lifecycle driver.
func (s *Server) StartWithContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	zap.L().Info("Starting HTTP server",
		zap.String("addr", s.httpServer.Addr),
	)

	// Start lifecycle-managed background services via the composition root.
	lcCtx, lcCancel := context.WithCancel(ctx)
	if s.lifecycle != nil {
		if err := s.lifecycle.Start(lcCtx); err != nil {
			lcCancel()
			return fmt.Errorf("lifecycle startup failed: %w", err)
		}
	}

	// Start server in a goroutine
	srvErr := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				srvErr <- fmt.Errorf("panic in server listen goroutine: %v\n%s", r, debug.Stack())
			}
		}()
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			srvErr <- err
		}
	}()

	select {
	case err := <-srvErr:
		// Server failed to start
		lcCancel()
		return fmt.Errorf("server listen error: %w", err)
	case <-ctx.Done():
		zap.L().Info("Shutting down server...")
	}

	// Cancel lifecycle context (signals background goroutines to stop)
	lcCancel()

	// Stop rate limiter cleanup goroutine
	if s.appRouter != nil {
		s.appRouter.Stop()
	}

	// Graceful shutdown with timeout — derived from
	// context.WithoutCancel(ctx) so a caller-driven cancellation
	// (e.g. SIGINT closes the cmd/server-owned sigCtx) does NOT
	// short-circuit the 30s drain budget. The parent ctx's VALUES
	// are preserved; only its cancellation is detached. Without this
	// decoupling a SIGINT during the drain would collapse the 30s
	// window to ≈0ms (because context.WithTimeout(ctx, 30s) propagates
	// the parent's cancellation — the original pre-P2-1 cmd/server
	// used context.Background() explicitly to dodge this). P2-1
	// regression fix (June 2026): re-establishes the same decoupling
	// declaratively via WithoutCancel.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer shutdownCancel()

	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		zap.L().Error("Server forced to shutdown", zap.Error(err))
		return fmt.Errorf("server shutdown error: %w", err)
	}

	// Stop lifecycle-managed background services.
	// context.WithoutCancel(ctx) here too — the 10s lifecycle stop
	// must not be force-cancelled the moment a SIGINT arrives.
	if s.lifecycle != nil {
		stopCtx, stopCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer stopCancel()
		if err := s.lifecycle.Stop(stopCtx); err != nil {
			zap.L().Error("lifecycle shutdown error", zap.Error(err))
		}
	}

	zap.L().Info("Server exited gracefully")
	return nil
}

// SetWorkerHandler wires internal worker routes into the server's router.
// Delegates to Router.SetWorkerHandler.
func (s *Server) SetWorkerHandler(h interface{ RegisterRoutes(*gin.RouterGroup) }) {
	s.appRouter.SetWorkerHandler(h)
}

// SetInternalMediaHandler wires the QDRANT-001 server-to-server media
// routes (POST /internal/v1/media/sync) into the server's
// router. Delegates to Router.SetInternalMediaHandler.
//
// QDRANT-001 closure: the production binding is supplied by the asset
// module's storage.Handler — see internal/app/bootstrap.go or whoever
// holds the assets module after WireRegistry runs.
func (s *Server) SetInternalMediaHandler(h MediaInternalRouter) {
	s.appRouter.SetInternalMediaHandler(h)
}

// GetRouter returns the gin router (for testing)
func (s *Server) GetRouter() *gin.Engine {
	return s.router
}

// ── PG-006 typed-port bridges (server-scoped) ────────────────────────────
//
// PG-006.1 (June 2026): the previous serverSecurityAdapter inline struct
// was deleted. The canonical concrete is
// internal/api/middleware.TokenSecurityAdapter (re-located from
// pkg/middleware round-2). The cfg-wrapping trio that lived in
// api/server.go + cmd/admin/gen_api_docs.go +
// internal/app/middleware_security_adapter.go is now collapsed into
// construction-site snapshots. Only the rate-limit and feature-flags
// inline adapters remain below (their canonical equivalents are NOT
// yet tracked under internal/api/middleware; a separate consolidation
// would promote them — out of scope for PG-006.1).

// serverRateLimitAdapter mirrors internal/app/middleware_security_adapter.go's
// middlewareRateLimitAdapter for the RateLimitPort surface (same nil-check
// shape as the production adapter).
type serverRateLimitAdapter struct{ cfg *config.Config }

func (a *serverRateLimitAdapter) RateLimitEnabled() bool {
	if a.cfg == nil {
		return false
	}
	return a.cfg.Security.RateLimitEnabled
}
func (a *serverRateLimitAdapter) RateLimitRequests() int {
	if a.cfg == nil {
		return 0
	}
	return a.cfg.Security.RateLimitRequests
}

// serverFeatureFlagsAdapter mirrors internal/app/middleware_security_adapter.go's
// middlewareFeatureFlagsAdapter for the FeatureFlagsPort surface (same nil-check
// shape as the production adapter).
type serverFeatureFlagsAdapter struct{ cfg *config.Config }

func (a *serverFeatureFlagsAdapter) ArtlistEnabled() bool {
	if a.cfg == nil {
		return false
	}
	return a.cfg.Features.ArtlistEnabled
}
func (a *serverFeatureFlagsAdapter) ScriptClipsEnabled() bool {
	if a.cfg == nil {
		return false
	}
	return a.cfg.Features.ScriptClipsEnabled
}
