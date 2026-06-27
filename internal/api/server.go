// Package api provides the HTTP server for the PipelineGen system.
package api

import (
	"context"
	"fmt"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// LifecycleManager is the minimal lifecycle contract the server needs.
// The composition root (internal/app) implements this to manage background
// services (job runner, dispatchers, channel monitors, etc.).
//
// QDRANT-005 (June 2026) closure: the interface now exposes AddProbe so
// the readiness barrier can be EXTENDED at runtime by callers that
// wire dependencies after the lifecycle is constructed (e.g. the
// Qdrant probe in cmd/server/main.go: now done via AddProbe instead of
// silently failing through a type-assertion interface{} downsizing).
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
	cfg        *config.Config
	router     *gin.Engine
	appRouter  *Router // reference to the Router for cleanup
	httpServer *http.Server
	lifecycle  LifecycleManager
}

// NewServer creates a new HTTP server with module registry support.
// workerHandler (optional) is wired into /internal/v1 routes and must be
// set *before* Setup() runs so the gin engine registers the routes.
// lifecycle (optional) is used for Start/Stop of background services.
// healthSvc (optional) is the application-layer health.Service; when nil,
// health endpoints return 503.
//
// QDRANT-route-constructor (June 2026, PR 3): outboxHandler and
// mediasearchHandler are passed as nil to preserve the original 5-arg
// signature; callers that need the /internal/v1/{outbox,media/search}
// routes registered MUST use NewServerWithHealth directly with the
// two new params populated. Production server passes them — see
// cmd/server/main.go. The legacy Server.SetOutboxHandler /
// Server.SetMediasearchHandler setters delegate to appRouter and are
// kept for back-compat but are NOT used by the production binary
// anymore.
func NewServer(
	cfg *config.Config,
	registry *Registry,
	workerHandler interface{ RegisterRoutes(*gin.RouterGroup) },
	internalMediaHandler MediaInternalRouter,
	lifecycle LifecycleManager,
) *Server {
	return NewServerWithHealth(cfg, registry, workerHandler, internalMediaHandler, nil, nil, lifecycle, nil, nil)
}

// NewServerWithHealth creates a new HTTP server with an optional
// health-check service (PR1 Health boundary, June 2026).
// codex/health-ready-contract (June 2026): added readyChecker parameter
// so /ready receives the real ReadyChecker instead of nil.
//
// QDRANT-route-constructor (June 2026, PR 3 in the post-Qdrant landing
// series): added outboxHandler + mediasearchHandler parameters. Both MUST
// be wired before router.Setup() runs so the WorkerAuth-protected
// /internal/v1/* routes register at construction time. The previous
// flow (cmd/server/main.go calling SetOutboxHandler/SetMediasearchHandler
// after NewServerWithHealth returned) silently dropped the routes
// because Setup() had already executed and would never run again.
//
// 8-dep-cap note: this constructor now takes 9 parameters, exceeding
// the canonical 8-dep cap enforced by scripts/ci-architectural-checks.sh
// (8.4 server constructor > 8 deps). Refactor candidate: a Handlers
// bundle struct (Worker/Media/Outbox/MediaSearch). Tracked per the
// canonical ratchet in architecture/current.yaml. The overage is
// accepted here because all new params are typed-port interfaces
// (zero-value/nil-friendly) and the alternative (re-introducing
// post-Setup setters) re-introduces the bug.
//
// PG-006 (June 2026): every typed-port field on RouterConfig must be
// constructed via an adapter because the api package cannot import
// `internal/app` (the canonical composition root for the adapters).
// server.go is the single production caller that lives OUTSIDE the
// app-root composition boundary, so it bridges via local inline
// adapters (5 lines each) that mirror the production wrappers in
// internal/app/middleware_security_adapter.go. The duplication is
// accepted by PG-006's "no compatibility layer" rule: there is no
// shared interface — the production adapter is the wire root, the
// server.go adapters are transport bridges. Drift between the two
// must be caught by a unit-test cross-comparison; a follow-up PR
// could promote the adapters to a more shareable location, but doing
// so would require lifting the layering rule.
func NewServerWithHealth(
	cfg *config.Config,
	registry *Registry,
	workerHandler interface{ RegisterRoutes(*gin.RouterGroup) },
	internalMediaHandler MediaInternalRouter,
	outboxHandler InternalOutboxRouter,
	mediasearchHandler InternalMediaSearchRouter,
	lifecycle LifecycleManager,
	healthSvc interface{},
	readyChecker *systemhealth.ReadyChecker,
) *Server {
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
		// wiring MUST happen here, before Setup() runs. Post-Setup setters
		// (Server.SetOutboxHandler / Server.SetMediasearchHandler) are
		// retained for back-compat ONLY and are NOT used by the production
		// server binary anymore — see cmd/server/main.go for the proof.
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
	// the cfg != nil branch above. Both branches register the routes
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
	}
}

// SetLifecycle wires a LifecycleManager after construction (for callers
// that don't pass it through NewServer).
func (s *Server) SetLifecycle(lc LifecycleManager) {
	s.lifecycle = lc
}

// Start starts the HTTP server. Background services are managed by the
// LifecycleManager — this method only handles the HTTP lifecycle.
func (s *Server) Start() error {
	zap.L().Info("Starting HTTP server",
		zap.String("addr", s.httpServer.Addr),
	)

	// Single signal-derived parent context for the entire server lifecycle.
	rootCtx, rootCancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer rootCancel()

	// Start lifecycle-managed background services via the composition root.
	lcCtx, lcCancel := context.WithCancel(rootCtx)
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
				zap.L().Error("panic in server listen goroutine", zap.Any("recover", r))
			}
			close(srvErr)
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
	case <-rootCtx.Done():
		zap.L().Info("Shutting down server...")
	}

	// Cancel lifecycle context (signals background goroutines to stop)
	lcCancel()

	// Stop rate limiter cleanup goroutine
	if s.appRouter != nil {
		s.appRouter.Stop()
	}

	// Graceful shutdown with timeout — created from a fresh background
	// context so it is NOT cancelled when rootCtx is. The 30s deadline
	// gives in-flight requests time to finish regardless of the signal
	// that triggered the shutdown.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		zap.L().Error("Server forced to shutdown", zap.Error(err))
		return fmt.Errorf("server shutdown error: %w", err)
	}

	// Stop lifecycle-managed background services.
	if s.lifecycle != nil {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
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
// routes (POST /internal/v1/media/sync-drive-folder) into the server's
// router. Delegates to Router.SetInternalMediaHandler.
//
// QDRANT-001 closure: the production binding is supplied by the asset
// module's storage.Handler — see internal/app/bootstrap.go or whoever
// holds the assets module after WireRegistry runs.
func (s *Server) SetInternalMediaHandler(h MediaInternalRouter) {
	s.appRouter.SetInternalMediaHandler(h)
}

// SetOutboxHandler wires the QDRANT-002 outbox monitoring handler onto
// the WorkerAuth-protected /internal/v1/outbox/* group.
// Delegates to Router.SetOutboxHandler.
//
// DEPRECATED (QDRANT-route-constructor, June 2026, PR 3):
// post-Setup wiring is unsafe because router.Setup() has already
// registered every route. The production binary delegates via
// NewServerWithHealth with outboxHandler passed in the constructor;
// callers that rely on this setter will silently 404 on
// /internal/v1/outbox/*. Kept only for back-compat with the
// pre-PR-3 binary that called it. Will be removed once
// cmd/server/main.go has been promoted long enough that a sweep
// of internal/api/** confirms no remaining caller.
func (s *Server) SetOutboxHandler(h InternalOutboxRouter) {
	if s.appRouter == nil {
		return
	}
	s.appRouter.SetOutboxHandler(h)
}

// SetMediasearchHandler wires the QDRANT-004 mediasearch handler onto
// the WorkerAuth-protected /internal/v1/media/search route.
// Delegates to Router.SetMediasearchHandler.
//
// DEPRECATED (QDRANT-route-constructor, June 2026, PR 3): see
// SetOutboxHandler doc for the deprecation rationale.
func (s *Server) SetMediasearchHandler(h InternalMediaSearchRouter) {
	if s.appRouter == nil {
		return
	}
	s.appRouter.SetMediasearchHandler(h)
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
func (a *serverFeatureFlagsAdapter) ScriptDocsEnabled() bool {
	if a.cfg == nil {
		return false
	}
	return a.cfg.Features.ScriptDocsEnabled
}
func (a *serverFeatureFlagsAdapter) ScriptClipsEnabled() bool {
	if a.cfg == nil {
		return false
	}
	return a.cfg.Features.ScriptClipsEnabled
}
